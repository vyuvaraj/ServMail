package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vyuvaraj/ServShared"
	"servmail/pkg/storage"
)

func setupTest() {
	rateLimitsMu.Lock()
	rateLimits = make(map[string][]time.Time)
	rateLimitsMu.Unlock()

	templateRepoMu.Lock()
	templateRepo = make(map[string]map[string]string)
	templateRepoMu.Unlock()

	trackingMu.Lock()
	trackingRepo = make(map[string]*storage.TrackingInfo)
	trackingMu.Unlock()

	preferencesMu.Lock()
	preferences = make(map[string]*storage.Preferences)
	preferencesMu.Unlock()

	attachmentsMu.Lock()
	attachmentsRepo = make(map[string]*storage.Attachment)
	attachmentsMu.Unlock()
}

func TestServMailTemplateAndChannels(t *testing.T) {
	setupTest()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/mail/send", handleSend)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// 1. Test Email Channel Template Rendering
	payload := storage.SendRequest{
		Channel:  "email",
		Target:   "admin@example.com",
		Template: "Hello {{.Name}}! Your code is {{.Code}}.",
		Context: map[string]interface{}{
			"Name": "Alice",
			"Code": 123456,
		},
	}
	body, _ := json.Marshal(payload)
	resp, err := http.Post(testServer.URL+"/api/mail/send", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("failed to post send request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected StatusOK, got %d", resp.StatusCode)
	}

	var res SendResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if res.Status != "delivered" || res.DeliveredTo != "admin@example.com" {
		t.Errorf("unexpected delivery response: %+v", res)
	}

	expectedBody := "Hello Alice! Your code is 123456."
	if res.Body != expectedBody {
		t.Errorf("expected rendered body %q, got %q", expectedBody, res.Body)
	}

	// 2. Test Slack Channel
	payload.Channel = "slack"
	payload.Target = "https://hooks.slack.com/services/123"
	body2, _ := json.Marshal(payload)
	resp2, err := http.Post(testServer.URL+"/api/mail/send", "application/json", bytes.NewReader(body2))
	if err != nil {
		t.Fatalf("failed Slack send request: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected StatusOK on Slack channel, got %d", resp2.StatusCode)
	}
}

func TestServMailRetriesAndDLQ(t *testing.T) {
	setupTest()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/mail/send", handleSend)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	payload := storage.SendRequest{
		Channel:  "email",
		Target:   "fail-mailbox@example.com", // will trigger failures
		Template: "Hello",
	}
	body, _ := json.Marshal(payload)
	resp, err := http.Post(testServer.URL+"/api/mail/send", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("failed to post send request: %v", err)
	}
	defer resp.Body.Close()

	// Should be StatusAccepted (queued in DLQ) on persistent failure
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("expected StatusAccepted on DLQ fallback, got %d", resp.StatusCode)
	}

	var res SendResponse
	_ = json.NewDecoder(resp.Body).Decode(&res)

	if res.Status != "queued_in_dlq" {
		t.Errorf("expected queued_in_dlq status, got %q", res.Status)
	}
}

func TestServMailRateLimiting(t *testing.T) {
	setupTest()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/mail/send", handleSend)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	payload := storage.SendRequest{
		Channel:  "email",
		Target:   "spammy-recipient@example.com",
		Template: "Hello",
	}
	body, _ := json.Marshal(payload)

	// Send 5 successful quick messages
	for i := 0; i < 5; i++ {
		resp, err := http.Post(testServer.URL+"/api/mail/send", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		resp.Body.Close()
	}

	// 6th message should be rate limited (Too Many Requests - 429)
	resp, err := http.Post(testServer.URL+"/api/mail/send", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("expected StatusTooManyRequests (429) on 6th request, got %d", resp.StatusCode)
	}
}

func TestServMailTemplateVersioning(t *testing.T) {
	setupTest()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/mail/send", handleSend)
	mux.HandleFunc("/api/mail/templates", handleRegisterTemplate)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// 1. Register a versioned template v1
	tmplPayload1 := map[string]string{
		"name":    "welcome-email",
		"version": "v1",
		"content": "Welcome v1 to {{.name}}!",
	}
	bodyTmpl1, _ := json.Marshal(tmplPayload1)
	respT1, err := http.Post(testServer.URL+"/api/mail/templates", "application/json", bytes.NewReader(bodyTmpl1))
	if err != nil || respT1.StatusCode != http.StatusCreated {
		t.Fatalf("failed to register template v1: %v", err)
	}
	respT1.Body.Close()

	// 2. Register a versioned template v2
	tmplPayload2 := map[string]string{
		"name":    "welcome-email",
		"version": "v2",
		"content": "Welcome v2 to {{.name}}! Enjoy your stay.",
	}
	bodyTmpl2, _ := json.Marshal(tmplPayload2)
	respT2, _ := http.Post(testServer.URL+"/api/mail/templates", "application/json", bytes.NewReader(bodyTmpl2))
	respT2.Body.Close()

	// 3. Send mail using template name and version v1
	sendPayload1 := storage.SendRequest{
		Channel:  "email",
		Target:   "user@example.com",
		Template: "welcome-email",
		Version:  "v1",
		Context:  map[string]interface{}{"name": "Servverse"},
	}
	bodyS1, _ := json.Marshal(sendPayload1)
	respS1, err := http.Post(testServer.URL+"/api/mail/send", "application/json", bytes.NewReader(bodyS1))
	if err != nil || respS1.StatusCode != http.StatusOK {
		t.Fatalf("send v1 failed: %v", err)
	}
	var resS1 SendResponse
	json.NewDecoder(respS1.Body).Decode(&resS1)
	respS1.Body.Close()

	if resS1.Body != "Welcome v1 to Servverse!" {
		t.Errorf("expected rendered v1 content, got %q", resS1.Body)
	}

	// 4. Send mail using template name and version v2
	sendPayload2 := storage.SendRequest{
		Channel:  "email",
		Target:   "user@example.com",
		Template: "welcome-email",
		Version:  "v2",
		Context:  map[string]interface{}{"name": "Servverse"},
	}
	bodyS2, _ := json.Marshal(sendPayload2)
	respS2, _ := http.Post(testServer.URL+"/api/mail/send", "application/json", bytes.NewReader(bodyS2))
	var resS2 SendResponse
	json.NewDecoder(respS2.Body).Decode(&resS2)
	respS2.Body.Close()

	if resS2.Body != "Welcome v2 to Servverse! Enjoy your stay." {
		t.Errorf("expected rendered v2 content, got %q", resS2.Body)
	}
}

func TestServMailTrackingAndPreferences(t *testing.T) {
	setupTest()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/mail/send", handleSend)
	mux.HandleFunc("/api/mail/tracking/", handleGetTracking)
	mux.HandleFunc("/api/mail/tracking/event", handlePostTrackingEvent)
	mux.HandleFunc("/api/mail/preferences", handlePreferences)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// 1. Send mail and extract tracking ID
	sendPayload := storage.SendRequest{
		Channel:  "email",
		Target:   "track-user@example.com",
		Template: "Hello Tracking!",
		Category: "marketing",
	}
	body, _ := json.Marshal(sendPayload)
	resp, err := http.Post(testServer.URL+"/api/mail/send", "application/json", bytes.NewReader(body))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("failed to send: %v", err)
	}
	var sendRes SendResponse
	json.NewDecoder(resp.Body).Decode(&sendRes)
	resp.Body.Close()

	if sendRes.MessageID == "" {
		t.Fatalf("expected valid message ID in response")
	}

	// 2. Query tracking status -> should be "sent"
	getResp, err := http.Get(testServer.URL + "/api/mail/tracking/" + sendRes.MessageID)
	if err != nil || getResp.StatusCode != http.StatusOK {
		t.Fatalf("failed to query tracking: %v", err)
	}
	var trackInfo storage.TrackingInfo
	json.NewDecoder(getResp.Body).Decode(&trackInfo)
	getResp.Body.Close()

	if trackInfo.Status != "sent" {
		t.Errorf("expected status 'sent', got %q", trackInfo.Status)
	}

	// 3. Post event -> open message
	eventPayload := map[string]string{
		"message_id": sendRes.MessageID,
		"status":     "opened",
	}
	eventBody, _ := json.Marshal(eventPayload)
	eventResp, err := http.Post(testServer.URL+"/api/mail/tracking/event", "application/json", bytes.NewReader(eventBody))
	if err != nil || eventResp.StatusCode != http.StatusOK {
		t.Fatalf("failed to post event: %v", err)
	}
	eventResp.Body.Close()

	// 4. Query tracking status again -> should be "opened"
	getResp2, _ := http.Get(testServer.URL + "/api/mail/tracking/" + sendRes.MessageID)
	var trackInfo2 storage.TrackingInfo
	json.NewDecoder(getResp2.Body).Decode(&trackInfo2)
	getResp2.Body.Close()

	if trackInfo2.Status != "opened" {
		t.Errorf("expected status 'opened', got %q", trackInfo2.Status)
	}

	// 5. Update preferences to opt-out of "marketing"
	prefPayload := storage.Preferences{
		Recipient: "track-user@example.com",
		OptedOut:  map[string]bool{"marketing": true},
	}
	prefBody, _ := json.Marshal(prefPayload)
	prefResp, err := http.Post(testServer.URL+"/api/mail/preferences", "application/json", bytes.NewReader(prefBody))
	if err != nil || prefResp.StatusCode != http.StatusOK {
		t.Fatalf("failed to update preferences: %v", err)
	}
	prefResp.Body.Close()

	// 6. Try sending marketing email again -> should fail with StatusForbidden (403)
	resp2, err := http.Post(testServer.URL+"/api/mail/send", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("failed send request: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusForbidden {
		t.Errorf("expected StatusForbidden (403) for opted out category, got %d", resp2.StatusCode)
	}
}

func TestServMailDashboard(t *testing.T) {
	setupTest()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/mail/send", handleSend)
	mux.HandleFunc("/api/mail/dashboard", handleMailDashboard)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// Reset trackingRepo for isolated test assertion
	trackingMu.Lock()
	trackingRepo = make(map[string]*storage.TrackingInfo)
	trackingMu.Unlock()

	// 1. Send first mail -> status "sent"
	sendPayload1 := storage.SendRequest{
		Channel:  "email",
		Target:   "dash-user@example.com",
		Template: "Welcome to dashboard tracking!",
	}
	body1, _ := json.Marshal(sendPayload1)
	resp1, err := http.Post(testServer.URL+"/api/mail/send", "application/json", bytes.NewReader(body1))
	if err != nil || resp1.StatusCode != http.StatusOK {
		t.Fatalf("first send failed: %v", err)
	}
	resp1.Body.Close()

	// 2. Query Dashboard -> metrics should align
	dashResp, err := http.Get(testServer.URL + "/api/mail/dashboard")
	if err != nil || dashResp.StatusCode != http.StatusOK {
		t.Fatalf("dashboard request failed: %v", err)
	}
	var metrics map[string]interface{}
	json.NewDecoder(dashResp.Body).Decode(&metrics)
	dashResp.Body.Close()

	if metrics["total_messages"].(float64) != 1 || metrics["sent"].(float64) != 1 {
		t.Errorf("unexpected dashboard stats: %+v", metrics)
	}
}

func TestServMailAttachmentsColdTier(t *testing.T) {
	setupTest()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/mail/attachments", handleUploadAttachment)
	mux.HandleFunc("/api/mail/attachments/", handleGetAttachment)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// 1. Upload small attachment -> should remain "local"
	smallPayload := map[string]string{
		"filename": "invoice.pdf",
		"payload":  "small-base64-payload-data",
	}
	bodySmall, _ := json.Marshal(smallPayload)
	respSmall, err := http.Post(testServer.URL+"/api/mail/attachments", "application/json", bytes.NewReader(bodySmall))
	if err != nil || respSmall.StatusCode != http.StatusCreated {
		t.Fatalf("small upload failed: %v", err)
	}
	var smallRes map[string]string
	json.NewDecoder(respSmall.Body).Decode(&smallRes)
	respSmall.Body.Close()

	if smallRes["storage"] != "local" {
		t.Errorf("expected small attachment to be stored 'local', got %q", smallRes["storage"])
	}

	// 2. Fetch small attachment and verify payload exists
	getSmall, err := http.Get(testServer.URL + "/api/mail/attachments/" + smallRes["id"])
	if err != nil || getSmall.StatusCode != http.StatusOK {
		t.Fatalf("fetch small failed: %v", err)
	}
	var attSmall storage.Attachment
	json.NewDecoder(getSmall.Body).Decode(&attSmall)
	getSmall.Body.Close()

	if attSmall.Payload != "small-base64-payload-data" {
		t.Errorf("unexpected payload: %q", attSmall.Payload)
	}

	// 3. Upload large attachment (>1MB simulation) -> should be evicted to "cold" tier
	largePayload := map[string]string{
		"filename": "backup.zip",
		"payload":  strings.Repeat("A", 10005),
	}
	bodyLarge, _ := json.Marshal(largePayload)
	respLarge, err := http.Post(testServer.URL+"/api/mail/attachments", "application/json", bytes.NewReader(bodyLarge))
	if err != nil || respLarge.StatusCode != http.StatusCreated {
		t.Fatalf("large upload failed: %v", err)
	}
	var largeRes map[string]string
	json.NewDecoder(respLarge.Body).Decode(&largeRes)
	respLarge.Body.Close()

	if largeRes["storage"] != "cold" {
		t.Errorf("expected large attachment to be evicted to 'cold' tier, got %q", largeRes["storage"])
	}

	// 4. Fetch large attachment and verify payload was evicted
	getLarge, err := http.Get(testServer.URL + "/api/mail/attachments/" + largeRes["id"])
	if err != nil || getLarge.StatusCode != http.StatusOK {
		t.Fatalf("fetch large failed: %v", err)
	}
	var attLarge storage.Attachment
	json.NewDecoder(getLarge.Body).Decode(&attLarge)
	getLarge.Body.Close()

	if attLarge.Payload != "" {
		t.Errorf("expected payload to be evicted, got %q", attLarge.Payload)
	}
}

func TestTableDrivenMailValidation(t *testing.T) {
	setupTest()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/mail/send", handleSend)
	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	tests := []struct {
		name       string
		channel    string
		target     string
		template   string
		wantStatus int
	}{
		{
			name:       "Missing Target",
			channel:    "email",
			target:     "",
			template:   "Hello {{.Name}}",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Unsupported Channel",
			channel:    "fax",
			target:     "123-456",
			template:   "Hello {{.Name}}",
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := storage.SendRequest{
				Channel:  tt.channel,
				Target:   tt.target,
				Template: tt.template,
			}
			body, _ := json.Marshal(payload)
			resp, err := http.Post(testServer.URL+"/api/mail/send", "application/json", bytes.NewReader(body))
			if err != nil {
				t.Fatalf("failed to make request: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tt.wantStatus {
				t.Errorf("expected status %d, got %d", tt.wantStatus, resp.StatusCode)
			}
		})
	}
}

func TestServMailMockSMTPServer(t *testing.T) {
	setupTest()
	
	storeClient := ServShared.NewStoreClient()
	tmplStore := storage.NewServStoreTemplateStore(storeClient)
	mockServer := NewMailServer("8094", tmplStore,
		&rateLimits, &rateLimitsMu,
		&templateRepo, &templateRepoMu,
		&trackingRepo, &trackingMu,
		&preferences, &preferencesMu,
		&attachmentsRepo, &attachmentsMu,
		"1026",
		&mockedEmails, &mockedEmailsMu)
		
	go mockServer.startMockSMTPServer()
	
	time.Sleep(50 * time.Millisecond)
	
	conn, err := net.Dial("tcp", "localhost:1026")
	if err != nil {
		t.Fatalf("failed to connect to mock SMTP: %v", err)
	}
	defer conn.Close()
	
	reader := bufio.NewReader(conn)
	greeting, _ := reader.ReadString('\n')
	if !strings.Contains(greeting, "220") {
		t.Errorf("unexpected greeting: %s", greeting)
	}
	
	conn.Write([]byte("HELO localhost\r\n"))
	_, _ = reader.ReadString('\n')
	
	conn.Write([]byte("MAIL FROM: sender@example.com\r\n"))
	_, _ = reader.ReadString('\n')
	
	conn.Write([]byte("RCPT TO: receiver@example.com\r\n"))
	_, _ = reader.ReadString('\n')
	
	conn.Write([]byte("DATA\r\n"))
	_, _ = reader.ReadString('\n')
	
	conn.Write([]byte("Subject: Test Subject\r\n\r\nTest Body content\r\n.\r\n"))
	_, _ = reader.ReadString('\n')
	
	conn.Write([]byte("QUIT\r\n"))
	
	time.Sleep(50 * time.Millisecond)
	
	mockedEmailsMu.RLock()
	length := len(mockedEmails)
	if length == 0 {
		t.Errorf("expected at least one captured email, got 0")
	} else {
		captured := mockedEmails[length-1]
		if !strings.Contains(captured.To, "receiver@example.com") {
			t.Errorf("expected to receiver@example.com, got %q", captured.To)
		}
		if !strings.Contains(captured.Subject, "Test Subject") {
			t.Errorf("expected Subject 'Test Subject', got %q", captured.Subject)
		}
	}
	mockedEmailsMu.RUnlock()
	
	mux := http.NewServeMux()
	mux.HandleFunc("/api/mail/mock-smtp", mockServer.handleGetMockEmails)
	testServer := httptest.NewServer(mux)
	defer testServer.Close()
	
	respGet, err := http.Get(testServer.URL + "/api/mail/mock-smtp")
	if err != nil || respGet.StatusCode != http.StatusOK {
		t.Fatalf("failed GET request: %v", err)
	}
	var retrieved []storage.MockEmail
	json.NewDecoder(respGet.Body).Decode(&retrieved)
	respGet.Body.Close()
	
	if len(retrieved) == 0 {
		t.Errorf("expected retrieved mock emails to not be empty")
	}
	
	reqClear, _ := http.NewRequest(http.MethodDelete, testServer.URL+"/api/mail/mock-smtp", nil)
	respClear, err := http.DefaultClient.Do(reqClear)
	if err != nil || respClear.StatusCode != http.StatusOK {
		t.Fatalf("failed DELETE request: %v", err)
	}
	respClear.Body.Close()
	
	mockedEmailsMu.RLock()
	if len(mockedEmails) != 0 {
		t.Errorf("expected mocked emails to be cleared, got %d", len(mockedEmails))
	}
	mockedEmailsMu.RUnlock()
}
