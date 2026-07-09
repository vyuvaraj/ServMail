package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/vyuvaraj/ServShared"
	"servmail/pkg/storage"
	mailtemplate "servmail/pkg/template"
)

var (
	EnterpriseSignDKIM = func(body string) string { return "" }
)

type HandlerContext struct {
	RateLimits      map[string][]time.Time
	RateLimitsMu    *sync.Mutex
	TemplateRepo    map[string]map[string]string
	TemplateRepoMu  *sync.RWMutex
	TrackingRepo    map[string]*storage.TrackingInfo
	TrackingMu      *sync.RWMutex
	Preferences     map[string]*storage.Preferences
	PreferencesMu   *sync.RWMutex
	AttachmentsRepo map[string]*storage.Attachment
	AttachmentsMu   *sync.RWMutex
	MockedEmails    *[]storage.MockEmail
	MockedEmailsMu  *sync.RWMutex
	TemplateStore   storage.TemplateStore
}

type SendResponse struct {
	MessageID   string `json:"message_id,omitempty"`
	Status      string `json:"status"`
	DeliveredTo string `json:"delivered_to"`
	Body        string `json:"body"`
}

func (ctx *HandlerContext) HandleSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req storage.SendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.Channel == "" || req.Target == "" || req.Template == "" {
		http.Error(w, "Channel, target, and template are required", http.StatusBadRequest)
		return
	}

	ctx.RateLimitsMu.Lock()
	now := time.Now()
	cutoff := now.Add(-1 * time.Minute)

	var active []time.Time
	for _, t := range ctx.RateLimits[req.Target] {
		if t.After(cutoff) {
			active = append(active, t)
		}
	}

	if len(active) >= 5 {
		ctx.RateLimitsMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate_limit_exceeded","message":"Recipient rate limit exceeded. Max 5 messages per minute."}`))
		return
	}

	active = append(active, now)
	ctx.RateLimits[req.Target] = active
	ctx.RateLimitsMu.Unlock()

	// Check recipient category preference
	category := req.Category
	if category == "" {
		category = "transactional"
	}
	ctx.PreferencesMu.RLock()
	pref, exists := ctx.Preferences[req.Target]
	if exists && pref.OptedOut[category] {
		ctx.PreferencesMu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":"opted_out","message":"Recipient has opted out of category: ` + category + `"}`))
		return
	}
	ctx.PreferencesMu.RUnlock()

	// 1. Resolve template content
	templateText := req.Template
	if req.Version != "" {
		ctx.TemplateRepoMu.RLock()
		versions, exists := ctx.TemplateRepo[req.Template]
		if exists {
			content, vExists := versions[req.Version]
			if vExists {
				templateText = content
			}
		}
		ctx.TemplateRepoMu.RUnlock()
	}

	bodyStr, err := mailtemplate.RenderTemplate(templateText, req.Context)
	if err != nil {
		http.Error(w, "Template execution/compile error: "+err.Error(), http.StatusBadRequest)
		return
	}

	// 2. Deliver via channel with retries
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
				dkimSig := EnterpriseSignDKIM(bodyStr)
				if dkimSig != "" {
					log.Printf("[ServMail] [DKIM] Added signature: %s", dkimSig)
				}
				log.Printf("[ServMail] [EMAIL] Sending to %s: %s", req.Target, bodyStr)
				ctx.MockedEmailsMu.Lock()
				*ctx.MockedEmails = append(*ctx.MockedEmails, storage.MockEmail{
					From:    "http-api@servmail",
					To:      req.Target,
					Subject: "Sent via HTTP API",
					Body:    bodyStr,
					Time:    time.Now(),
				})
				ctx.MockedEmailsMu.Unlock()
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
		
		ctx.TrackingMu.Lock()
		ctx.TrackingRepo[msgID] = &storage.TrackingInfo{
			MessageID:   msgID,
			Status:      "bounced",
			DeliveredTo: req.Target,
			UpdatedAt:   time.Now(),
		}
		ctx.TrackingMu.Unlock()

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

	ctx.TrackingMu.Lock()
	ctx.TrackingRepo[msgID] = &storage.TrackingInfo{
		MessageID:   msgID,
		Status:      "sent",
		DeliveredTo: req.Target,
		UpdatedAt:   time.Now(),
	}
	ctx.TrackingMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(SendResponse{
		MessageID:   msgID,
		Status:      "delivered",
		DeliveredTo: req.Target,
		Body:        bodyStr,
	})
}

func (ctx *HandlerContext) HandleRegisterTemplate(w http.ResponseWriter, r *http.Request) {
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

	ctx.TemplateRepoMu.Lock()
	versions, exists := ctx.TemplateRepo[req.Name]
	if !exists {
		versions = make(map[string]string)
		ctx.TemplateRepo[req.Name] = versions
	}
	versions[req.Version] = req.Content
	ctx.TemplateRepoMu.Unlock()

	if ctx.TemplateStore != nil {
		copied := make(map[string]map[string]string)
		ctx.TemplateRepoMu.RLock()
		for k, v := range ctx.TemplateRepo {
			copiedInner := make(map[string]string)
			for k2, v2 := range v {
				copiedInner[k2] = v2
			}
			copied[k] = copiedInner
		}
		ctx.TemplateRepoMu.RUnlock()
		_ = ctx.TemplateStore.SaveTemplates(copied)
	}

	_ = ServShared.EmitAuditEvent("ServMail", "TEMPLATE_REGISTER", "system", map[string]interface{}{"name": req.Name, "version": req.Version})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	w.Write([]byte(`{"status":"success","message":"Template version registered successfully"}`))
}

func (ctx *HandlerContext) HandleGetTracking(w http.ResponseWriter, r *http.Request) {
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

	ctx.TrackingMu.RLock()
	info, exists := ctx.TrackingRepo[msgID]
	ctx.TrackingMu.RUnlock()

	if !exists {
		http.Error(w, "Tracking info not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(info)
}

func (ctx *HandlerContext) HandlePostTrackingEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		MessageID string `json:"message_id"`
		Status    string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	ctx.TrackingMu.Lock()
	info, exists := ctx.TrackingRepo[req.MessageID]
	if exists {
		info.Status = req.Status
		info.UpdatedAt = time.Now()
	}
	ctx.TrackingMu.Unlock()

	if !exists {
		http.Error(w, "Message not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success","message":"Event tracked successfully"}`))
}

func (ctx *HandlerContext) HandlePreferences(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		recipient := r.URL.Query().Get("recipient")
		if recipient == "" {
			ctx.PreferencesMu.RLock()
			var list []*storage.Preferences
			for _, p := range ctx.Preferences {
				list = append(list, p)
			}
			ctx.PreferencesMu.RUnlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(list)
			return
		}

		ctx.PreferencesMu.RLock()
		pref, exists := ctx.Preferences[recipient]
		ctx.PreferencesMu.RUnlock()

		if !exists {
			pref = &storage.Preferences{
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
		var req storage.Preferences
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid payload", http.StatusBadRequest)
			return
		}

		if req.Recipient == "" {
			http.Error(w, "Recipient is required", http.StatusBadRequest)
			return
		}

		ctx.PreferencesMu.Lock()
		ctx.Preferences[req.Recipient] = &req
		ctx.PreferencesMu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"success","message":"Preferences updated successfully"}`))
		return
	}

	http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
}

func (ctx *HandlerContext) HandleMailDashboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx.TrackingMu.RLock()
	totalSent := 0
	totalBounced := 0
	totalOpened := 0
	for _, info := range ctx.TrackingRepo {
		switch info.Status {
		case "sent":
			totalSent++
		case "bounced":
			totalBounced++
		case "opened":
			totalOpened++
		}
	}
	ctx.TrackingMu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"total_messages": len(ctx.TrackingRepo),
		"sent":           totalSent,
		"bounced":        totalBounced,
		"opened":         totalOpened,
	})
}

func (ctx *HandlerContext) HandleUploadAttachment(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		ctx.AttachmentsMu.RLock()
		var list []*storage.Attachment
		for _, a := range ctx.AttachmentsRepo {
			list = append(list, a)
		}
		ctx.AttachmentsMu.RUnlock()
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
		Payload  string `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	size := int64(len(req.Payload))
	storageType := "local"
	payload := req.Payload

	if size > 10000 {
		storageType = "cold"
		payload = ""
	}

	id := fmt.Sprintf("att-%d", time.Now().UnixNano())

	ctx.AttachmentsMu.Lock()
	ctx.AttachmentsRepo[id] = &storage.Attachment{
		ID:        id,
		Filename:  req.Filename,
		SizeBytes: size,
		Storage:   storageType,
		Payload:   payload,
	}
	ctx.AttachmentsMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{
		"id":      id,
		"storage": storageType,
		"status":  "success",
	})
}

func (ctx *HandlerContext) HandleGetAttachment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	parts := strings.Split(r.URL.Path, "/")
	id := parts[len(parts)-1]

	ctx.AttachmentsMu.RLock()
	att, exists := ctx.AttachmentsRepo[id]
	ctx.AttachmentsMu.RUnlock()

	if !exists {
		http.Error(w, "Attachment not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(att)
}

func (ctx *HandlerContext) HandleGetMockEmails(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		ctx.MockedEmailsMu.RLock()
		defer ctx.MockedEmailsMu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(*ctx.MockedEmails)
		return
	}
	if r.Method == http.MethodDelete {
		ctx.MockedEmailsMu.Lock()
		defer ctx.MockedEmailsMu.Unlock()
		*ctx.MockedEmails = []storage.MockEmail{}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"success","message":"Mock emails cleared"}`))
		return
	}
	http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
}
