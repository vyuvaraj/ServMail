package storage

import (
	"testing"
)

func TestNewServStoreTemplateStore(t *testing.T) {
	s := NewServStoreTemplateStore(nil)
	if s == nil {
		t.Fatal("expected NewServStoreTemplateStore to return initialized struct")
	}
	if s.Client != nil {
		t.Error("expected Client to be nil")
	}
}

func TestLoadTemplatesMissingClient(t *testing.T) {
	s := NewServStoreTemplateStore(nil)
	_, err := s.LoadTemplates()
	if err == nil {
		t.Error("expected error loading with nil client")
	}
}

func TestSaveTemplatesMissingClient(t *testing.T) {
	s := NewServStoreTemplateStore(nil)
	err := s.SaveTemplates(nil)
	if err == nil {
		t.Error("expected error saving with nil client")
	}
}

func TestAttachmentFields(t *testing.T) {
	att := Attachment{
		ID:        "att-1",
		Filename:  "test.pdf",
		SizeBytes: 1024,
		Storage:   "cold",
		Payload:   "data",
	}
	if att.ID != "att-1" || att.Filename != "test.pdf" || att.SizeBytes != 1024 || att.Storage != "cold" || att.Payload != "data" {
		t.Errorf("incorrect fields: %+v", att)
	}
}

func TestPreferencesFields(t *testing.T) {
	pref := Preferences{
		Recipient: "user@example.com",
		OptedOut:  map[string]bool{"marketing": true},
	}
	if pref.Recipient != "user@example.com" || !pref.OptedOut["marketing"] {
		t.Errorf("incorrect preferences: %+v", pref)
	}
}
