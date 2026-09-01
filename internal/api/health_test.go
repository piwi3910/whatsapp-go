package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/piwi3910/whatsapp-go/whatsapp"
)

// fakeService reports a fixed connection state. The embedded nil interface
// supplies the rest of whatsapp.Service; the probe handlers only ever call
// Status, and any other call would panic loudly rather than pass silently.
type fakeService struct {
	whatsapp.Service
	state string
}

func (f fakeService) Status() whatsapp.ConnectionStatus {
	return whatsapp.ConnectionStatus{State: f.state}
}

// decodeData unwraps the {"ok":..., "data":{...}} envelope every handler
// writes. Note the envelope's "ok" means "well-formed response", not
// "ready" — the HTTP status code carries readiness.
func decodeData(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var env struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("response body is not JSON: %v (%s)", err, rec.Body.String())
	}
	return env.Data
}

func probeServer(state string) *Server {
	return &Server{
		client:    fakeService{state: state},
		version:   "test",
		startTime: time.Now(),
	}
}

func TestLivenessAlwaysOK(t *testing.T) {
	// Liveness must not depend on the session: a logged-out process is
	// degraded, not wedged, and killing it does not help.
	for _, state := range []string{"connected", "connecting", "disconnected", "logged_out"} {
		rec := httptest.NewRecorder()
		probeServer(state).handleLiveness(rec, httptest.NewRequest(http.MethodGet, "/api/v1/healthz", nil))
		if rec.Code != http.StatusOK {
			t.Errorf("state %q: healthz = %d, want 200", state, rec.Code)
		}
	}
}

func TestReadinessReflectsConnectionState(t *testing.T) {
	tests := []struct {
		state string
		code  int
		ready bool
	}{
		{"connected", http.StatusOK, true},
		{"connecting", http.StatusServiceUnavailable, false},
		{"disconnected", http.StatusServiceUnavailable, false},
		{"logged_out", http.StatusServiceUnavailable, false},
	}
	for _, tc := range tests {
		t.Run(tc.state, func(t *testing.T) {
			s := probeServer(tc.state)
			rec := httptest.NewRecorder()
			s.handleReadiness(rec, httptest.NewRequest(http.MethodGet, "/api/v1/readyz", nil))
			if rec.Code != tc.code {
				t.Errorf("readyz = %d, want %d", rec.Code, tc.code)
			}
			body := decodeData(t, rec)
			if body["ready"] != tc.ready {
				t.Errorf("ready = %v, want %v", body["ready"], tc.ready)
			}
			// The failing case must say why, so an operator can tell a
			// pending connect apart from an expired session.
			if body["state"] != tc.state {
				t.Errorf("state = %v, want %q", body["state"], tc.state)
			}
		})
	}
}

// The legacy endpoint used to return 200 unconditionally; it must now tell
// the truth while keeping its original response fields.
func TestLegacyHealthReportsTruth(t *testing.T) {
	rec := httptest.NewRecorder()
	probeServer("connected").handleHealth(rec, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("health (connected) = %d, want 200", rec.Code)
	}

	rec = httptest.NewRecorder()
	probeServer("logged_out").handleHealth(rec, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("health (logged_out) = %d, want 503", rec.Code)
	}
	body := decodeData(t, rec)
	for _, key := range []string{"state", "uptime_seconds", "version"} {
		if _, ok := body[key]; !ok {
			t.Errorf("health response lost the %q field", key)
		}
	}
}

func TestMetricsExposesConnectionGauge(t *testing.T) {
	s := probeServer("connected")
	s.reqTotal.Add(7)
	rec := httptest.NewRecorder()
	s.handleMetrics(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{"wa_connected 1", "wa_up 1", "wa_http_requests_total 7"} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics output missing %q:\n%s", want, body)
		}
	}

	rec = httptest.NewRecorder()
	probeServer("disconnected").handleMetrics(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rec.Body.String(), "wa_connected 0") {
		t.Error("disconnected client should report wa_connected 0")
	}
}

// Stop() runs on the signal goroutine and used to read a field that Start()
// wrote, so a SIGTERM racing startup nil-panicked. The http.Server is now
// built in the constructor: stopping before starting is a no-op.
func TestStopBeforeStartDoesNotPanic(t *testing.T) {
	s := NewServer(fakeService{state: "disconnected"}, nil, nil, "k", "test", 0)
	if err := s.Stop(); err != nil {
		t.Errorf("Stop before Start returned %v, want nil", err)
	}
	if err := s.Stop(); err != nil {
		t.Errorf("second Stop returned %v, want nil", err)
	}
}

// Under -race this catches the Start/Stop data race directly.
func TestStopRacingStart(t *testing.T) {
	s := NewServer(fakeService{state: "connected"}, nil, nil, "k", "test", 0)

	errCh := make(chan error, 1)
	go func() { errCh <- s.Start("127.0.0.1", 0, "", "") }()
	// No synchronisation on purpose: Stop may land before, during or after
	// the listener comes up, which is exactly the SIGTERM-at-startup case.
	if err := s.Stop(); err != nil {
		t.Errorf("Stop returned %v", err)
	}

	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			t.Errorf("Start returned %v", err)
		}
	case <-time.After(5 * time.Second):
		// Stop can win the race before Serve is called, leaving Start
		// blocked; shut it down again so the test does not hang forever.
		_ = s.Stop()
		select {
		case <-errCh:
		case <-time.After(5 * time.Second):
			t.Fatal("Start never returned after Stop")
		}
	}
}

// Probe and metrics routes must stay reachable without an API key: a k8s
// kubelet cannot present one.
func TestProbeRoutesNeedNoAuth(t *testing.T) {
	s := NewServer(fakeService{state: "connected"}, nil, nil, "secret", "test", 0)
	for path, want := range map[string]int{
		"/api/v1/healthz": http.StatusOK,
		"/api/v1/readyz":  http.StatusOK,
		"/api/v1/health":  http.StatusOK,
		"/metrics":        http.StatusOK,
	} {
		rec := httptest.NewRecorder()
		s.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != want {
			t.Errorf("GET %s = %d, want %d", path, rec.Code, want)
		}
		if rec.Header().Get(requestIDHeader) == "" {
			t.Errorf("GET %s: no %s response header", path, requestIDHeader)
		}
	}
}

func TestRequestIDIsPreserved(t *testing.T) {
	s := NewServer(fakeService{state: "connected"}, nil, nil, "secret", "test", 0)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/healthz", nil)
	req.Header.Set(requestIDHeader, "caller-supplied-id")
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)
	if got := rec.Header().Get(requestIDHeader); got != "caller-supplied-id" {
		t.Errorf("%s = %q, want the incoming id echoed back", requestIDHeader, got)
	}
}
