package storage

import (
	"encoding/json"
	"errors"
	"log"
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

type MockEmail struct {
	To      string    `json:"to"`
	From    string    `json:"from"`
	Subject string    `json:"subject"`
	Body    string    `json:"body"`
	Time    time.Time `json:"time"`
}

type TemplateStore interface {
	LoadTemplates() (map[string]map[string]string, error)
	SaveTemplates(templates map[string]map[string]string) error
}

type ServStoreTemplateStore struct {
	Client *ServShared.StoreClient
}

func NewServStoreTemplateStore(client *ServShared.StoreClient) *ServStoreTemplateStore {
	return &ServStoreTemplateStore{Client: client}
}

func (s *ServStoreTemplateStore) LoadTemplates() (map[string]map[string]string, error) {
	if s.Client == nil {
		return nil, errors.New("store client not initialized")
	}
	data, err := s.Client.Get("serv-mail-templates", "templates.json")
	if err != nil {
		return nil, err
	}
	var templates map[string]map[string]string
	if err := json.Unmarshal(data, &templates); err != nil {
		return nil, err
	}
	log.Printf("[PERSISTENCE] Loaded %d templates from ServStore", len(templates))
	return templates, nil
}

func (s *ServStoreTemplateStore) SaveTemplates(templates map[string]map[string]string) error {
	if s.Client == nil {
		return errors.New("store client not initialized")
	}
	data, err := json.Marshal(templates)
	if err != nil {
		return err
	}
	return s.Client.Put("serv-mail-templates", "templates.json", data)
}
