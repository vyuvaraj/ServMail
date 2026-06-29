package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/vyuvaraj/ServShared"
)

type SendRequest struct {
	Channel  string                 `json:"channel"`  // email, slack, sms
	Target   string                 `json:"target"`   // email address, webhook URL, or phone number
	Template string                 `json:"template"` // Go template text or registered name
	Version  string                 `json:"version"`  // Optional template version
	Category string                 `json:"category"`  // e.g. "marketing", "transactional", "alerts"
	Context  map[string]interface{} `json:"context"`  // template variables
}

type TrackingInfo struct {
	MessageID   string    `json:"message_id"`
	Status      string    `json:"status"` // sent, opened, clicked, bounced
	DeliveredTo string    `json:"delivered_to"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Preferences struct {
	Recipient string          `json:"recipient"`
	OptedOut  map[string]bool `json:"opted_out"` // category -> is_opted_out
}

type Attachment struct {
	ID        string `json:"id"`
	Filename  string `json:"filename"`
	SizeBytes int64  `json:"size_bytes"`
	Storage   string `json:"storage"` // local, cold
	Payload   string `json:"payload,omitempty"`
}

var (
	rateLimits      = make(map[string][]time.Time)
	rateLimitsMu    sync.Mutex
	templateRepo    = make(map[string]map[string]string) // name -> version -> content
	templateRepoMu  sync.RWMutex
	trackingRepo    = make(map[string]*TrackingInfo)
	trackingMu      sync.RWMutex
	preferences     = make(map[string]*Preferences)
	preferencesMu   sync.RWMutex
	attachmentsRepo = make(map[string]*Attachment)
	attachmentsMu   sync.RWMutex
)

type SendResponse struct {
	MessageID   string `json:"message_id,omitempty"`
	Status      string `json:"status"`
	DeliveredTo string `json:"delivered_to"`
	Body        string `json:"body"`
}

func main() {
	portStr := flag.String("port", "8094", "ServMail server port")
	flag.Parse()

	port := os.Getenv("PORT")
	if port == "" {
		port = *portStr
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	mux.HandleFunc("/api/mail/send", handleSend)
	mux.HandleFunc("/api/mail/templates", handleRegisterTemplate)
	mux.HandleFunc("/api/mail/tracking/", handleGetTracking)
	mux.HandleFunc("/api/mail/tracking/event", handlePostTrackingEvent)
	mux.HandleFunc("/api/mail/preferences", handlePreferences)
	mux.HandleFunc("/api/mail/dashboard", handleMailDashboard)
	mux.HandleFunc("/api/mail/attachments", handleUploadAttachment)
	mux.HandleFunc("/api/mail/attachments/", handleGetAttachment)

	serverHandler := ServShared.AuthMiddleware(mux)

	log.Printf("ServMail server starting on port %s", port)
	if err := http.ListenAndServe(":"+port, serverHandler); err != nil {
		log.Fatalf("failed to start ServMail: %v", err)
	}
}

func handleSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req SendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.Channel == "" || req.Target == "" || req.Template == "" {
		http.Error(w, "Channel, target, and template are required", http.StatusBadRequest)
		return
	}

	rateLimitsMu.Lock()
	now := time.Now()
	cutoff := now.Add(-1 * time.Minute)

	var active []time.Time
	for _, t := range rateLimits[req.Target] {
		if t.After(cutoff) {
			active = append(active, t)
		}
	}

	if len(active) >= 5 {
		rateLimitsMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate_limit_exceeded","message":"Recipient rate limit exceeded. Max 5 messages per minute."}`))
		return
	}

	active = append(active, now)
	rateLimits[req.Target] = active
	rateLimitsMu.Unlock()

	// Check recipient category preference
	category := req.Category
	if category == "" {
		category = "transactional"
	}
	preferencesMu.RLock()
	pref, exists := preferences[req.Target]
	if exists && pref.OptedOut[category] {
		preferencesMu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":"opted_out","message":"Recipient has opted out of category: ` + category + `"}`))
		return
	}
	preferencesMu.RUnlock()

	// 1. Resolve template content
	templateText := req.Template
	if req.Version != "" {
		templateRepoMu.RLock()
		versions, exists := templateRepo[req.Template]
		if exists {
			content, vExists := versions[req.Version]
			if vExists {
				templateText = content
			}
		}
		templateRepoMu.RUnlock()
	}

	tmpl, err := template.New("notification").Parse(templateText)
	if err != nil {
		http.Error(w, "Template compile error: "+err.Error(), http.StatusBadRequest)
		return
	}

	var renderedBody bytes.Buffer
	if err := tmpl.Execute(&renderedBody, req.Context); err != nil {
		http.Error(w, "Template execution error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	bodyStr := renderedBody.String()

	// 2. Deliver via channel with retries (simulate temporary failures if target contains "fail")
	channelLower := strings.ToLower(req.Channel)
	var deliveryErr error
	maxAttempts := 3
	backoff := 10 * time.Millisecond

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		deliveryErr = nil
		if strings.Contains(req.Target, "fail") {
			deliveryErr = fmt.Errorf("temporary network failure on attempt %d", attempt)
		}

		if deliveryErr == nil {
			switch channelLower {
			case "email":
				log.Printf("[ServMail] [EMAIL] Sending to %s: %s", req.Target, bodyStr)
			case "slack":
				log.Printf("[ServMail] [SLACK] Posting to webhook %s: %s", req.Target, bodyStr)
			case "sms":
				log.Printf("[ServMail] [SMS] Sending to number %s: %s", req.Target, bodyStr)
			default:
				http.Error(w, "Unsupported delivery channel: "+req.Channel, http.StatusBadRequest)
				return
			}
			break
		}

		log.Printf("[ServMail] Attempt %d failed: %v. Retrying in %v...", attempt, deliveryErr, backoff)
		time.Sleep(backoff)
		backoff *= 2
	}

	msgID := fmt.Sprintf("msg-%d", time.Now().UnixNano())

	if deliveryErr != nil {
		dlqMsgID := fmt.Sprintf("mail-%d", time.Now().UnixNano())
		log.Printf("[DLQ] Published message to dead letter queue: %s (reason: %v)", dlqMsgID, deliveryErr)
		
		trackingMu.Lock()
		trackingRepo[msgID] = &TrackingInfo{
			MessageID:   msgID,
			Status:      "bounced",
			DeliveredTo: req.Target,
			UpdatedAt:   time.Now(),
		}
		trackingMu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(SendResponse{
			MessageID:   msgID,
			Status:      "queued_in_dlq",
			DeliveredTo: req.Target,
			Body:        bodyStr,
		})
		return
	}

	trackingMu.Lock()
	trackingRepo[msgID] = &TrackingInfo{
		MessageID:   msgID,
		Status:      "sent",
		DeliveredTo: req.Target,
		UpdatedAt:   time.Now(),
	}
	trackingMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(SendResponse{
		MessageID:   msgID,
		Status:      "delivered",
		DeliveredTo: req.Target,
		Body:        bodyStr,
	})
}

func handleRegisterTemplate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	if req.Name == "" || req.Version == "" || req.Content == "" {
		http.Error(w, "Name, version, and content are required", http.StatusBadRequest)
		return
	}

	templateRepoMu.Lock()
	versions, exists := templateRepo[req.Name]
	if !exists {
		versions = make(map[string]string)
		templateRepo[req.Name] = versions
	}
	versions[req.Version] = req.Content
	templateRepoMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	w.Write([]byte(`{"status":"success","message":"Template version registered successfully"}`))
}

func handleGetTracking(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	path := r.URL.Path
	var msgID string
	fmt.Sscanf(path, "/api/mail/tracking/%s", &msgID)
	if msgID == "" {
		http.Error(w, "Message ID is required", http.StatusBadRequest)
		return
	}

	trackingMu.RLock()
	info, exists := trackingRepo[msgID]
	trackingMu.RUnlock()

	if !exists {
		http.Error(w, "Tracking info not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(info)
}

func handlePostTrackingEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		MessageID string `json:"message_id"`
		Status    string `json:"status"` // opened, clicked, bounced
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	trackingMu.Lock()
	info, exists := trackingRepo[req.MessageID]
	if exists {
		info.Status = req.Status
		info.UpdatedAt = time.Now()
	}
	trackingMu.Unlock()

	if !exists {
		http.Error(w, "Message not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success","message":"Event tracked successfully"}`))
}

func handlePreferences(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		recipient := r.URL.Query().Get("recipient")
		if recipient == "" {
			preferencesMu.RLock()
			var list []*Preferences
			for _, p := range preferences {
				list = append(list, p)
			}
			preferencesMu.RUnlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(list)
			return
		}

		preferencesMu.RLock()
		pref, exists := preferences[recipient]
		preferencesMu.RUnlock()

		if !exists {
			pref = &Preferences{
				Recipient: recipient,
				OptedOut:  make(map[string]bool),
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(pref)
		return
	}

	if r.Method == http.MethodPost {
		var req Preferences
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid payload", http.StatusBadRequest)
			return
		}

		if req.Recipient == "" {
			http.Error(w, "Recipient is required", http.StatusBadRequest)
			return
		}

		preferencesMu.Lock()
		preferences[req.Recipient] = &req
		preferencesMu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"success","message":"Preferences updated successfully"}`))
		return
	}

	http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
}

func handleMailDashboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	trackingMu.RLock()
	totalSent := 0
	totalBounced := 0
	totalOpened := 0
	for _, info := range trackingRepo {
		switch info.Status {
		case "sent":
			totalSent++
		case "bounced":
			totalBounced++
		case "opened":
			totalOpened++
		}
	}
	trackingMu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"total_messages": len(trackingRepo),
		"sent":           totalSent,
		"bounced":        totalBounced,
		"opened":         totalOpened,
	})
}

func handleUploadAttachment(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		attachmentsMu.RLock()
		var list []*Attachment
		for _, a := range attachmentsRepo {
			list = append(list, a)
		}
		attachmentsMu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(list)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Filename string `json:"filename"`
		Payload  string `json:"payload"` // Base64 encoded payload
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	size := int64(len(req.Payload))
	storage := "local"
	payload := req.Payload

	if size > 10000 {
		storage = "cold"
		payload = ""
	}

	id := fmt.Sprintf("att-%d", time.Now().UnixNano())

	attachmentsMu.Lock()
	attachmentsRepo[id] = &Attachment{
		ID:        id,
		Filename:  req.Filename,
		SizeBytes: size,
		Storage:   storage,
		Payload:   payload,
	}
	attachmentsMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{
		"id":      id,
		"storage": storage,
		"status":  "success",
	})
}

func handleGetAttachment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	parts := strings.Split(r.URL.Path, "/")
	id := parts[len(parts)-1]

	attachmentsMu.RLock()
	att, exists := attachmentsRepo[id]
	attachmentsMu.RUnlock()

	if !exists {
		http.Error(w, "Attachment not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(att)
}
