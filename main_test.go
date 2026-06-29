package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServMailTemplateAndChannels(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/mail/send", handleSend)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// 1. Test Email Channel Template Rendering
	payload := SendRequest{
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
	resp2, _ := http.Post(testServer.URL+"/api/mail/send", "application/json", bytes.NewReader(body2))
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected StatusOK on Slack channel, got %d", resp2.StatusCode)
	}
}

func TestServMailRetriesAndDLQ(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/mail/send", handleSend)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	payload := SendRequest{
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
	mux := http.NewServeMux()
	mux.HandleFunc("/api/mail/send", handleSend)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	payload := SendRequest{
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
	sendPayload1 := SendRequest{
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
	sendPayload2 := SendRequest{
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
