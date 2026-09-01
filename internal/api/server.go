package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/piwi3910/whatsapp-go/internal/store"
	"github.com/piwi3910/whatsapp-go/internal/webhook"
	"github.com/piwi3910/whatsapp-go/whatsapp"
)

// requestIDHeader carries a correlation id between the caller, this service
// and whatever it logs to. An incoming value is preserved so a trace started
// at an ingress or by the calling agent survives the hop.
const requestIDHeader = "X-Request-Id"

type ctxKey int

const requestIDKey ctxKey = 0

// RequestIDFromContext returns the correlation id assigned to r's request,
// or "" outside a request.
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// Server is the REST API server.
type Server struct {
	router chi.Router
	// httpServer is built in NewServer and never reassigned. Stop() runs on
	// the signal goroutine and used to race Start()'s assignment of this
	// field — a SIGTERM arriving before the listener was up dereferenced nil
	// and panicked the process during shutdown.
	httpServer *http.Server
	client     whatsapp.Service
	store      *store.Store
	dispatcher *webhook.Dispatcher
	apiKey     string
	login      *loginState
	startTime  time.Time
	version    string

	// Counters for the dependency-free /metrics surface. Written from every
	// request goroutine, so they must be atomic.
	reqTotal    atomic.Uint64
	reqErrors   atomic.Uint64 // responses with status >= 500
	readyProbes atomic.Uint64
	notReady    atomic.Uint64
}

// NewServer creates a new API server.
func NewServer(svc whatsapp.Service, st *store.Store, disp *webhook.Dispatcher, apiKey, version string, maxUploadSize int64) *Server {
	s := &Server{
		client:     svc,
		store:      st,
		dispatcher: disp,
		apiKey:     apiKey,
		login:      &loginState{},
		version:    version,
		startTime:  time.Now(),
	}

	r := chi.NewRouter()
	// recoverer sits innermost so the 500 it writes still unwinds back
	// through requestLogger and countRequests. With it outermost, a
	// panicking request produced no access-log line and never incremented
	// wa_http_server_errors_total — the one class of failure you most need
	// to see was the one class that was invisible.
	r.Use(s.requestID)
	r.Use(requestLogger)
	r.Use(s.countRequests)
	r.Use(recoverer)

	// Probe and metrics endpoints — no auth required.
	//
	// healthz is liveness: it answers as long as the process can serve HTTP.
	// readyz is readiness: it answers 200 only when WhatsApp is actually
	// connected, so an orchestrator stops routing to a process whose session
	// died. Keeping them separate is what prevents a lost session from
	// looking like a crash (and being restart-looped) or, worse, a dead
	// session from looking healthy.
	r.Get("/api/v1/healthz", s.handleLiveness)
	r.Get("/api/v1/readyz", s.handleReadiness)
	// Retained for backwards compatibility with existing callers; now
	// reports the truth (503 when not connected) instead of a blanket 200.
	r.Get("/api/v1/health", s.handleHealth)
	r.Get("/metrics", s.handleMetrics)

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
		r.Get("/api/v1/auth/login", s.handleLoginStatus)
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
	s.httpServer = &http.Server{
		Handler: r,
		// Bound the time a peer may take to send headers. Without it a
		// handful of idle connections can pin the process indefinitely.
		// Deliberately no ReadTimeout/WriteTimeout: media uploads and
		// downloads are legitimately slow and a whole-request deadline
		// would truncate them.
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return s
}

// Start begins listening. Blocks until the server is stopped.
// Start begins listening. Blocks until the server is stopped.
// When tlsCert and tlsKey are both non-empty the server serves TLS
// (issue #25); otherwise plain HTTP.
func (s *Server) Start(host string, port int, tlsCert, tlsKey string) error {
	addr := fmt.Sprintf("%s:%d", host, port)
	if tlsCert != "" && tlsKey != "" {
		slog.Info("api server listening (TLS)", "addr", addr, "version", s.version)
		return s.httpServer.ListenAndServeTLS(tlsCert, tlsKey)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	slog.Info("api server listening", "addr", addr, "version", s.version)
	return s.httpServer.Serve(ln)
}

// Stop gracefully shuts down the server. Safe to call before Start and safe
// to call twice.
func (s *Server) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.httpServer.Shutdown(ctx)
}

// requestID assigns (or adopts) a correlation id and echoes it back so a
// caller can tie its own logs to ours.
func (s *Server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(requestIDHeader)
		if id == "" {
			id = newRequestID()
		}
		w.Header().Set(requestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// An id is diagnostic, not load-bearing: never fail a request over it.
		return "unknown"
	}
	return hex.EncodeToString(b[:])
}

// countRequests feeds the /metrics counters.
func (s *Server) countRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.reqTotal.Add(1)
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		if sw.status >= 500 {
			s.reqErrors.Add(1)
		}
	})
}

// handleMetrics exposes a minimal Prometheus text-format surface. Written by
// hand rather than with the client library so the module gains no new
// dependency for what is a dozen numbers.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	connected := 0
	if s.client.Status().State == stateConnected {
		connected = 1
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	fmt.Fprintf(w, "# HELP wa_up Always 1; presence of the metric means the process is serving.\n")
	fmt.Fprintf(w, "# TYPE wa_up gauge\nwa_up 1\n")
	fmt.Fprintf(w, "# HELP wa_connected 1 when the WhatsApp session is connected.\n")
	fmt.Fprintf(w, "# TYPE wa_connected gauge\nwa_connected %d\n", connected)
	fmt.Fprintf(w, "# HELP wa_uptime_seconds Seconds since the API server was constructed.\n")
	fmt.Fprintf(w, "# TYPE wa_uptime_seconds gauge\nwa_uptime_seconds %d\n", int(time.Since(s.startTime).Seconds()))
	fmt.Fprintf(w, "# HELP wa_http_requests_total HTTP requests handled.\n")
	fmt.Fprintf(w, "# TYPE wa_http_requests_total counter\nwa_http_requests_total %d\n", s.reqTotal.Load())
	fmt.Fprintf(w, "# HELP wa_http_server_errors_total HTTP responses with status >= 500.\n")
	fmt.Fprintf(w, "# TYPE wa_http_server_errors_total counter\nwa_http_server_errors_total %d\n", s.reqErrors.Load())
	fmt.Fprintf(w, "# HELP wa_readiness_probes_total Readiness probes served.\n")
	fmt.Fprintf(w, "# TYPE wa_readiness_probes_total counter\nwa_readiness_probes_total %d\n", s.readyProbes.Load())
	fmt.Fprintf(w, "# HELP wa_readiness_failures_total Readiness probes answered 503.\n")
	fmt.Fprintf(w, "# TYPE wa_readiness_failures_total counter\nwa_readiness_failures_total %d\n", s.notReady.Load())
}
