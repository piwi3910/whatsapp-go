package api

import (
	"context"
	"encoding/base64"
	"net/http"
	"time"

	"github.com/skip2/go-qrcode"
)

// stateConnected is the ConnectionStatus.State value that means the WhatsApp
// socket is up and messages can flow. Everything else ("connecting",
// "disconnected", "logged_out") means this replica cannot do its job.
const stateConnected = "connected"

// handleLiveness answers the liveness probe. It is intentionally
// unconditional: liveness asks "is this process wedged?", and the answer is
// no as long as the HTTP server can still route a request. Reporting session
// trouble here would make an orchestrator kill and restart the pod, which
// neither restores a lost session nor recovers a disconnect any faster than
// the in-process supervisor does — it just loses the buffered state.
func (s *Server) handleLiveness(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// handleReadiness answers the readiness probe: 200 only when the WhatsApp
// session is actually connected, 503 otherwise with the state in the body.
// This is what takes a logged-out or disconnected replica out of service
// instead of leaving it advertised as healthy while silently dropping work.
func (s *Server) handleReadiness(w http.ResponseWriter, r *http.Request) {
	s.readyProbes.Add(1)
	status := s.client.Status()
	ready := status.State == stateConnected
	code := http.StatusOK
	if !ready {
		s.notReady.Add(1)
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, map[string]any{
		"ready":          ready,
		"state":          status.State,
		"uptime_seconds": int(time.Since(s.startTime).Seconds()),
		"version":        s.version,
	})
}

// handleHealth is the pre-existing endpoint, kept for backwards
// compatibility. It behaves as a readiness check: it used to return 200
// unconditionally, which meant a monitor could never tell a working replica
// from one whose session had expired.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	status := s.client.Status()
	code := http.StatusOK
	if status.State != stateConnected {
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, map[string]any{
		"state":          status.State,
		"ready":          status.State == stateConnected,
		"uptime_seconds": int(time.Since(s.startTime).Seconds()),
		"version":        s.version,
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	// Pairing outlives this request: the caller scans the QR code well after the
	// handler has returned, so the QR channel must not be bound to r.Context().
	qrChan, err := s.client.Login(context.Background())
	if err != nil {
		writeError(w, http.StatusBadRequest, "LOGIN_ERROR", err.Error())
		return
	}

	// Wait for first QR code
	evt, ok := <-qrChan
	if !ok || evt.Done {
		writeError(w, http.StatusInternalServerError, "LOGIN_ERROR", "login channel closed unexpectedly")
		return
	}

	// Generate QR code image as base64
	qrPNG, err := qrcode.Encode(evt.Code, qrcode.Medium, 256)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "QR_ERROR", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"qr_code_base64": base64.StdEncoding.EncodeToString(qrPNG),
		"qr_code_text":   evt.Code,
		"timeout":        60,
	})

	// Continue processing QR events in background (waiting for scan)
	go func() {
		for range qrChan {
			// Drain remaining events
		}
	}()
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.client.Logout(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "LOGOUT_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.client.Status())
}
