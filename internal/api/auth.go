package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"image/png"
	"net/http"
	"sync"
	"time"

	"github.com/boombuler/barcode/qr"
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

// handleLogin starts a pairing attempt and returns the first QR code. The
// attempt outlives this request; refreshed codes and the terminal outcome
// are tracked on the server and polled via GET /api/v1/auth/login (issue #7)
// — a slow phone gets a re-issued code without re-POSTing.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	// Pairing outlives this request: the caller scans the QR code well after the
	// handler has returned, so the QR channel must not be bound to r.Context().
	qrChan, err := s.client.Login(context.Background())
	if err != nil {
		// ErrLoginInProgress / ErrAlreadyLoggedIn classify as 409 CONFLICT;
		// anything else keeps its mapped status.
		writeServiceError(w, r, "login", err)
		return
	}

	// Wait for first QR code
	evt, ok := <-qrChan
	if !ok || evt.Done {
		writeError(w, http.StatusInternalServerError, "LOGIN_ERROR", "login channel closed unexpectedly")
		return
	}

	// Generate QR code image as base64. boombuler/barcode replaces the
	// unmaintained skip2/go-qrcode pin (issue #20); M error correction
	// (15% recovery) is plenty for the short pairing string.
	img, err := qr.Encode(evt.Code, qr.M, qr.Auto)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "QR_ERROR", err.Error())
		return
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		writeError(w, http.StatusInternalServerError, "QR_ERROR", err.Error())
		return
	}

	s.login.setAttempt(evt.Code)

	writeJSON(w, http.StatusOK, map[string]any{
		"qr_code_base64": base64.StdEncoding.EncodeToString(buf.Bytes()),
		"qr_code_text":   evt.Code,
	})

	// Follow the pairing in the background, keeping the tracked state fresh.
	// Refreshed codes, success, timeout and pairing errors all land in
	// loginState so GET /api/v1/auth/login can report them.
	go func() {
		for e := range qrChan {
			switch {
			case e.Err != nil:
				s.login.setTerminal("error", e.Err.Error())
			case e.Done:
				s.login.setTerminal("success", "")
			default:
				s.login.setCode(e.Code)
			}
		}
	}()
}

// handleLoginStatus reports the outcome of the in-flight pairing attempt
// (issue #7): the current QR code (possibly re-issued after a rotation) and
// the terminal state, so API clients can poll instead of re-POSTing login.
func (s *Server) handleLoginStatus(w http.ResponseWriter, r *http.Request) {
	code, state, attempt, errMsg := s.login.snapshot()
	if state == "" {
		writeError(w, http.StatusNotFound, "NO_LOGIN_IN_PROGRESS", "no pairing attempt in progress; POST /api/v1/auth/login to start one")
		return
	}
	out := map[string]any{"state": state, "attempt": attempt}
	if code != "" {
		out["qr_code_text"] = code
		if img, err := qr.Encode(code, qr.M, qr.Auto); err == nil {
			var buf bytes.Buffer
			if png.Encode(&buf, img) == nil {
				out["qr_code_base64"] = base64.StdEncoding.EncodeToString(buf.Bytes())
			}
		}
	}
	if errMsg != "" {
		out["error"] = errMsg
	}
	writeJSON(w, http.StatusOK, out)
}

// loginState tracks the in-flight pairing attempt. Login is single-flight on
// the client, so there is exactly one attempt at a time; the fields are
// guarded by mu, never by atomicity tricks.
type loginState struct {
	mu      sync.Mutex
	code    string
	state   string // "" (idle) | "pending" | "success" | "timeout" | "error"
	attempt int
	errMsg  string
}

func (l *loginState) setAttempt(code string) {
	l.mu.Lock()
	l.code, l.state, l.attempt, l.errMsg = code, "pending", 1, ""
	l.mu.Unlock()
}

func (l *loginState) setCode(code string) {
	l.mu.Lock()
	l.code, l.attempt = code, l.attempt+1
	l.mu.Unlock()
}

func (l *loginState) setTerminal(state, errMsg string) {
	l.mu.Lock()
	l.state, l.errMsg = state, errMsg
	l.mu.Unlock()
}

func (l *loginState) snapshot() (code, state string, attempt int, errMsg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.code, l.state, l.attempt, l.errMsg
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
