package api

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/piwi3910/whatsapp-go/internal/client"
	"github.com/piwi3910/whatsapp-go/internal/store"
	"github.com/piwi3910/whatsapp-go/internal/webhook"
)

// Server is the REST API server.
type Server struct {
	router     chi.Router
	httpServer *http.Server
	client     client.Service
	store      *store.Store
	dispatcher *webhook.Dispatcher
	apiKey     string
	startTime  time.Time
	version    string
}

// NewServer creates a new API server.
func NewServer(svc client.Service, st *store.Store, disp *webhook.Dispatcher, apiKey, version string, maxUploadSize int64) *Server {
	s := &Server{
		client:     svc,
		store:      st,
		dispatcher: disp,
		apiKey:     apiKey,
		version:    version,
		startTime:  time.Now(),
	}

	r := chi.NewRouter()
	r.Use(recoverer)
	r.Use(requestLogger)

	// Health endpoint — no auth required
	r.Get("/api/v1/health", s.handleHealth)

	// Authenticated routes
	r.Group(func(r chi.Router) {
		r.Use(apiKeyAuth(apiKey))
		if maxUploadSize > 0 {
			r.Use(func(next http.Handler) http.Handler {
				return http.MaxBytesHandler(next, maxUploadSize)
			})
		}

		// Auth
		r.Post("/api/v1/auth/login", s.handleLogin)
		r.Post("/api/v1/auth/logout", s.handleLogout)
		r.Get("/api/v1/auth/status", s.handleAuthStatus)

		// Messages
		r.Post("/api/v1/messages/send", s.handleSendMessage)
		r.Get("/api/v1/messages", s.handleListMessages)
		r.Get("/api/v1/messages/{id}", s.handleGetMessage)
		r.Delete("/api/v1/messages/{id}", s.handleDeleteMessage)
		r.Post("/api/v1/messages/{id}/react", s.handleReactMessage)
		r.Post("/api/v1/messages/{id}/read", s.handleMarkRead)

		// Groups
		r.Post("/api/v1/groups", s.handleCreateGroup)
		r.Get("/api/v1/groups", s.handleListGroups)
		r.Get("/api/v1/groups/{jid}", s.handleGetGroup)
		r.Post("/api/v1/groups/{jid}/leave", s.handleLeaveGroup)
		r.Get("/api/v1/groups/{jid}/invite-link", s.handleGetInviteLink)
		r.Post("/api/v1/groups/join", s.handleJoinGroup)
		r.Post("/api/v1/groups/{jid}/participants/add", s.handleAddParticipants)
		r.Post("/api/v1/groups/{jid}/participants/remove", s.handleRemoveParticipants)
		r.Post("/api/v1/groups/{jid}/participants/promote", s.handlePromoteParticipants)
		r.Post("/api/v1/groups/{jid}/participants/demote", s.handleDemoteParticipants)

		// Contacts
		r.Get("/api/v1/contacts", s.handleListContacts)
		r.Get("/api/v1/contacts/{jid}", s.handleGetContact)
		r.Post("/api/v1/contacts/{jid}/block", s.handleBlockContact)
		r.Post("/api/v1/contacts/{jid}/unblock", s.handleUnblockContact)

		// Media
		r.Post("/api/v1/media/upload", s.handleUploadMedia)
		r.Get("/api/v1/media/{messageId}", s.handleDownloadMedia)

		// Webhooks
		r.Post("/api/v1/webhooks", s.handleCreateWebhook)
		r.Get("/api/v1/webhooks", s.handleListWebhooks)
		r.Delete("/api/v1/webhooks/{id}", s.handleDeleteWebhook)

		// Events
		r.Get("/api/v1/events", s.handleListEvents)
	})

	s.router = r
	return s
}

// Start begins listening. Blocks until the server is stopped.
func (s *Server) Start(host string, port int) error {
	addr := fmt.Sprintf("%s:%d", host, port)
	s.httpServer = &http.Server{
		Addr:    addr,
		Handler: s.router,
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	log.Printf("API server listening on %s", addr)
	return s.httpServer.Serve(ln)
}

// Stop gracefully shuts down the server.
func (s *Server) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.httpServer.Shutdown(ctx)
}
