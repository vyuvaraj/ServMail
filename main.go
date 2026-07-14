package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"strconv"
	"github.com/vyuvaraj/ServShared"
	"servmail/pkg/delivery"
	"servmail/pkg/handlers"
	"servmail/pkg/queue"
	"servmail/pkg/storage"
)

var (
	rateLimits      = make(map[string][]time.Time)
	rateLimitsMu    sync.Mutex
	templateRepo    = make(map[string]map[string]string)
	templateRepoMu  sync.RWMutex
	trackingRepo    = make(map[string]*storage.TrackingInfo)
	trackingMu      sync.RWMutex
	preferences     = make(map[string]*storage.Preferences)
	preferencesMu   sync.RWMutex
	attachmentsRepo = make(map[string]*storage.Attachment)
	attachmentsMu   sync.RWMutex

	mockedEmails   = []storage.MockEmail{}
	mockedEmailsMu sync.RWMutex

	templateStore storage.TemplateStore
	defaultServer *MailServer
	mailDiskQueue *queue.DiskQueue
)

type MailServer struct {
	port            string
	templateStore   storage.TemplateStore
	rateLimits      *map[string][]time.Time
	rateLimitsMu    *sync.Mutex
	templateRepo    *map[string]map[string]string
	templateRepoMu  *sync.RWMutex
	trackingRepo    *map[string]*storage.TrackingInfo
	trackingMu      *sync.RWMutex
	preferences     *map[string]*storage.Preferences
	preferencesMu   *sync.RWMutex
	attachmentsRepo *map[string]*storage.Attachment
	attachmentsMu   *sync.RWMutex
	mockSMTPPort    string
	mockedEmails    *[]storage.MockEmail
	mockedEmailsMu  *sync.RWMutex
}

func NewMailServer(port string, store storage.TemplateStore,
	rateLimits *map[string][]time.Time, rateLimitsMu *sync.Mutex,
	templateRepo *map[string]map[string]string, templateRepoMu *sync.RWMutex,
	trackingRepo *map[string]*storage.TrackingInfo, trackingMu *sync.RWMutex,
	preferences *map[string]*storage.Preferences, preferencesMu *sync.RWMutex,
	attachmentsRepo *map[string]*storage.Attachment, attachmentsMu *sync.RWMutex,
	mockSMTPPort string, mockedEmails *[]storage.MockEmail, mockedEmailsMu *sync.RWMutex) *MailServer {
	return &MailServer{
		port:            port,
		templateStore:   store,
		rateLimits:      rateLimits,
		rateLimitsMu:    rateLimitsMu,
		templateRepo:    templateRepo,
		templateRepoMu:  templateRepoMu,
		trackingRepo:    trackingRepo,
		trackingMu:      trackingMu,
		preferences:     preferences,
		preferencesMu:   preferencesMu,
		attachmentsRepo: attachmentsRepo,
		attachmentsMu:   attachmentsMu,
		mockSMTPPort:    mockSMTPPort,
		mockedEmails:    mockedEmails,
		mockedEmailsMu:  mockedEmailsMu,
	}
}

func initStore() {
	client := ServShared.NewStoreClient()
	templateStore = storage.NewServStoreTemplateStore(client)
	loadTemplatesFromStore()

	queuePath := os.Getenv("SERVMAIL_QUEUE_PATH")
	if queuePath == "" {
		queuePath = "servmail-queue.jsonl"
	}
	mailDiskQueue = queue.NewDiskQueue(queuePath)
	log.Printf("[INFO] ServMail disk queue initialized: %s", queuePath)

	retentionDaysStr := os.Getenv("SERVMAIL_RETENTION_DAYS")
	if retentionDaysStr != "" {
		if days, err := strconv.Atoi(retentionDaysStr); err == nil && days > 0 {
			go func() {
				ticker := time.NewTicker(30 * time.Minute)
				defer ticker.Stop()
				retentionLimit := time.Duration(days) * 24 * time.Hour
				mailDiskQueue.EnforceRetention(retentionLimit)
				for range ticker.C {
					mailDiskQueue.EnforceRetention(retentionLimit)
				}
			}()
		}
	}
}

func loadTemplatesFromStore() {
	if loaded, err := templateStore.LoadTemplates(); err == nil {
		templateRepoMu.Lock()
		templateRepo = loaded
		templateRepoMu.Unlock()
	}
}

func getContext() *handlers.HandlerContext {
	return &handlers.HandlerContext{
		RateLimits:      rateLimits,
		RateLimitsMu:    &rateLimitsMu,
		TemplateRepo:    templateRepo,
		TemplateRepoMu:  &templateRepoMu,
		TrackingRepo:    trackingRepo,
		TrackingMu:      &trackingMu,
		Preferences:     preferences,
		PreferencesMu:   &preferencesMu,
		AttachmentsRepo: attachmentsRepo,
		AttachmentsMu:   &attachmentsMu,
		MockedEmails:    &mockedEmails,
		MockedEmailsMu:  &mockedEmailsMu,
		DiskQueue:       mailDiskQueue,
	}
}

type SendResponse = handlers.SendResponse

func (s *MailServer) handleGetMockEmails(w http.ResponseWriter, r *http.Request) {
	getContext().HandleGetMockEmails(w, r)
}


func handleSend(w http.ResponseWriter, r *http.Request) {
	getContext().HandleSend(w, r)
}

func handleRegisterTemplate(w http.ResponseWriter, r *http.Request) {
	getContext().HandleRegisterTemplate(w, r)
}

func handleGetTracking(w http.ResponseWriter, r *http.Request) {
	getContext().HandleGetTracking(w, r)
}

func handlePostTrackingEvent(w http.ResponseWriter, r *http.Request) {
	getContext().HandlePostTrackingEvent(w, r)
}

func handlePreferences(w http.ResponseWriter, r *http.Request) {
	getContext().HandlePreferences(w, r)
}

func handleMailDashboard(w http.ResponseWriter, r *http.Request) {
	getContext().HandleMailDashboard(w, r)
}

func handleUploadAttachment(w http.ResponseWriter, r *http.Request) {
	getContext().HandleUploadAttachment(w, r)
}

func handleGetAttachment(w http.ResponseWriter, r *http.Request) {
	getContext().HandleGetAttachment(w, r)
}

func handleGetMockEmails(w http.ResponseWriter, r *http.Request) {
	getContext().HandleGetMockEmails(w, r)
}

func (s *MailServer) startMockSMTPServer() {
	addr := ":" + s.mockSMTPPort
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Printf("[SMTP MOCK] Failed to start SMTP mock server on port %s: %v", s.mockSMTPPort, err)
		return
	}
	defer listener.Close()
	log.Printf("[SMTP MOCK] SMTP mock server listening on port %s", s.mockSMTPPort)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("[SMTP MOCK] Accept error: %v", err)
			continue
		}
		go delivery.HandleSMTPConnection(conn, s.mockedEmails, s.mockedEmailsMu)
	}
}

func main() {
	portStr := flag.String("port", "8094", "ServMail server port")
	mockSMTPPortStr := flag.String("mock-smtp-port", "1025", "Port to start the offline mock SMTP server")
	flag.Parse()

	port := os.Getenv("PORT")
	if port == "" {
		port = *portStr
	}

	mockSMTPPort := os.Getenv("MOCK_SMTP_PORT")
	if mockSMTPPort == "" {
		mockSMTPPort = *mockSMTPPortStr
	}

	standalone := ServShared.IsStandalone()
	if standalone {
		log.Println("[INFO] ServMail: Running in standalone mode. Store persistence redirected to local directory.")
	}

	initStore()

	defaultServer = NewMailServer(port, templateStore,
		&rateLimits, &rateLimitsMu,
		&templateRepo, &templateRepoMu,
		&trackingRepo, &trackingMu,
		&preferences, &preferencesMu,
		&attachmentsRepo, &attachmentsMu,
		mockSMTPPort, &mockedEmails, &mockedEmailsMu)

	go defaultServer.startMockSMTPServer()

	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/api/version", ServShared.VersionHandler("servmail", "1.0.0"))
	mux.HandleFunc("/api/v1/version", ServShared.VersionHandler("servmail", "1.0.0"))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	mux.HandleFunc("/api/mail/send", handleSend)
	mux.HandleFunc("/api/v1/mail/send", handleSend)
	mux.HandleFunc("/api/mail/templates", handleRegisterTemplate)
	mux.HandleFunc("/api/v1/mail/templates", handleRegisterTemplate)
	mux.HandleFunc("/api/mail/tracking/", handleGetTracking)
	mux.HandleFunc("/api/v1/mail/tracking/", handleGetTracking)
	mux.HandleFunc("/api/mail/tracking/event", handlePostTrackingEvent)
	mux.HandleFunc("/api/v1/mail/tracking/event", handlePostTrackingEvent)
	mux.HandleFunc("/api/mail/preferences", handlePreferences)
	mux.HandleFunc("/api/v1/mail/preferences", handlePreferences)
	mux.HandleFunc("/api/mail/dashboard", handleMailDashboard)
	mux.HandleFunc("/api/v1/mail/dashboard", handleMailDashboard)
	mux.HandleFunc("/api/mail/attachments", handleUploadAttachment)
	mux.HandleFunc("/api/v1/mail/attachments", handleUploadAttachment)
	mux.HandleFunc("/api/mail/attachments/", handleGetAttachment)
	mux.HandleFunc("/api/v1/mail/attachments/", handleGetAttachment)
	mux.HandleFunc("/api/mail/mock-smtp", handleGetMockEmails)
	mux.HandleFunc("/api/v1/mail/mock-smtp", handleGetMockEmails)

	serverHandler := ServShared.TraceMiddleware("servmail", ServShared.AuthMiddleware(mux))

	server := &http.Server{
		Addr:    ":" + port,
		Handler: serverHandler,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("[INFO] ServMail server starting on port %s", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("failed to start ServMail: %v", err)
		}
	}()

	<-stop

	log.Println("[INFO] Shutting down ServMail server...")
	ServShared.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server shutdown failed: %v", err)
	}
	log.Println("[INFO] ServMail server exited cleanly")
}
