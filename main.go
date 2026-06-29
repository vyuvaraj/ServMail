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
	Context  map[string]interface{} `json:"context"`  // template variables
}

var (
	rateLimits     = make(map[string][]time.Time)
	rateLimitsMu   sync.Mutex
	templateRepo   = make(map[string]map[string]string) // name -> version -> content
	templateRepoMu sync.RWMutex
)

type SendResponse struct {
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

	if deliveryErr != nil {
		dlqMsgID := fmt.Sprintf("mail-%d", time.Now().UnixNano())
		log.Printf("[DLQ] Published message to dead letter queue: %s (reason: %v)", dlqMsgID, deliveryErr)
		
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(SendResponse{
			Status:      "queued_in_dlq",
			DeliveredTo: req.Target,
			Body:        bodyStr,
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(SendResponse{
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
