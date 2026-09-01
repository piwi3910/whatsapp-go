package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/piwi3910/whatsapp-go/whatsapp"
)

// Issue #7: re-issued QR codes and the terminal outcome of a pairing attempt
// must be visible to API clients through GET /api/v1/auth/login, not only in
// the one-shot POST response.

type qrStub struct {
	whatsapp.Service
	events []whatsapp.QREvent
}

func (s *qrStub) Login(ctx context.Context) (<-chan whatsapp.QREvent, error) {
	out := make(chan whatsapp.QREvent, len(s.events))
	for _, e := range s.events {
		out <- e
	}
	close(out)
	return out, nil
}

func getLoginStatus(t *testing.T, s *Server) map[string]any {
	t.Helper()
	rec := do(s, authRequest(http.MethodGet, "/api/v1/auth/login", nil))
	if rec.Code == http.StatusTooManyRequests {
		// The status endpoint shares the global rate limit; back off and retry
		// instead of hammering it until the test itself is the DoS.
		time.Sleep(500 * time.Millisecond)
		return getLoginStatus(t, s)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/auth/login = %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data map[string]any `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	return body.Data
}

func TestLoginStatusNoAttempt(t *testing.T) {
	s := testServer(t, &stubService{})

	rec := do(s, authRequest(http.MethodGet, "/api/v1/auth/login", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 with no pairing attempt in progress", rec.Code)
	}
}

// waitForLogin polls the status endpoint until cond holds or the deadline expires.
func waitForLogin(t *testing.T, s *Server, cond func(map[string]any) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond(getLoginStatus(t, s)) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out waiting for login status")
}

func TestLoginStatusTracksReissuedCodeAndSuccess(t *testing.T) {
	s := testServer(t, &qrStub{events: []whatsapp.QREvent{
		{Code: "CODE-1"},
		{Code: "CODE-2"}, // rotated code
		{Done: true},
	}})

	rec := do(s, authRequest(http.MethodPost, "/api/v1/auth/login", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/auth/login = %d: %s", rec.Code, rec.Body.String())
	}
	var posted struct {
		Data map[string]any `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&posted); err != nil || posted.Data["qr_code_text"] != "CODE-1" {
		t.Fatalf("POST body = %v (want first code CODE-1)", posted.Data)
	}

	waitForLogin(t, s, func(m map[string]any) bool {
		return m["state"] == "success"
	})
	final := getLoginStatus(t, s)
	if final["qr_code_text"] != "CODE-2" {
		t.Errorf("tracked code = %v, want the re-issued CODE-2", final["qr_code_text"])
	}
	if attempt, _ := final["attempt"].(float64); attempt != 2 {
		t.Errorf("attempt = %v, want 2 (one code rotation)", final["attempt"])
	}
}

func TestLoginStatusReportsPairingError(t *testing.T) {
	s := testServer(t, &qrStub{events: []whatsapp.QREvent{
		{Code: "CODE-1"},
		{Done: true, Err: fmt.Errorf("device limit reached")},
	}})

	rec := do(s, authRequest(http.MethodPost, "/api/v1/auth/login", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/auth/login = %d: %s", rec.Code, rec.Body.String())
	}

	waitForLogin(t, s, func(m map[string]any) bool {
		return m["state"] == "error"
	})
	final := getLoginStatus(t, s)
	if final["error"] != "device limit reached" {
		t.Errorf("error = %v, want the pairing error text", final["error"])
	}
}
