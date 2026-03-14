package api

import (
	"encoding/base64"
	"net/http"
	"time"

	"github.com/skip2/go-qrcode"
)

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	status := s.client.Status()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"state":          status.State,
		"uptime_seconds": int(time.Since(s.startTime).Seconds()),
		"version":        s.version,
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	qrChan, err := s.client.Login()
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

	writeJSON(w, http.StatusOK, map[string]interface{}{
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
	if err := s.client.Logout(); err != nil {
		writeError(w, http.StatusInternalServerError, "LOGOUT_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.client.Status())
}
