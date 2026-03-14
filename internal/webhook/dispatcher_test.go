package webhook

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

func TestDispatch_MatchingEvent(t *testing.T) {
	var received []byte
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		received, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	d := New()
	d.Register(models.Webhook{
		ID:     "wh1",
		URL:    srv.URL,
		Events: []string{"message.received"},
	})

	d.Dispatch(models.Event{
		Type:      "message.received",
		Payload:   `{"text":"hello"}`,
		Timestamp: time.Now().Unix(),
	})

	// Wait for async delivery
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if received == nil {
		t.Fatal("webhook was not called")
	}

	var body map[string]any
	json.Unmarshal(received, &body)
	if body["event"] != "message.received" {
		t.Errorf("event = %v, want message.received", body["event"])
	}
}

func TestDispatch_WildcardSubscription(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	}))
	defer srv.Close()

	d := New()
	d.Register(models.Webhook{ID: "wh1", URL: srv.URL, Events: []string{"*"}})

	d.Dispatch(models.Event{
		Type: "group.created", Payload: "{}", Timestamp: time.Now().Unix(),
	})

	time.Sleep(100 * time.Millisecond)
	if !called {
		t.Error("wildcard webhook was not called")
	}
}

func TestDispatch_NonMatchingEvent(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	}))
	defer srv.Close()

	d := New()
	d.Register(models.Webhook{ID: "wh1", URL: srv.URL, Events: []string{"message.sent"}})

	d.Dispatch(models.Event{
		Type: "message.received", Payload: "{}", Timestamp: time.Now().Unix(),
	})

	time.Sleep(100 * time.Millisecond)
	if called {
		t.Error("webhook should not have been called for non-matching event")
	}
}

func TestDispatch_HMACSignature(t *testing.T) {
	var sigHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sigHeader = r.Header.Get("X-Wa-Signature")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	d := New()
	d.Register(models.Webhook{
		ID: "wh1", URL: srv.URL, Events: []string{"*"}, Secret: "mysecret",
	})

	d.Dispatch(models.Event{
		Type: "test", Payload: "{}", Timestamp: time.Now().Unix(),
	})

	time.Sleep(100 * time.Millisecond)
	if sigHeader == "" {
		t.Error("HMAC signature header missing")
	}
	if len(sigHeader) < 10 || sigHeader[:7] != "sha256=" {
		t.Errorf("signature format wrong: %q", sigHeader)
	}
}

func TestUnregister(t *testing.T) {
	d := New()
	d.Register(models.Webhook{ID: "wh1", URL: "http://example.com", Events: []string{"*"}})
	d.Unregister("wh1")

	// Should have no webhooks
	if len(d.webhooks) != 0 {
		t.Errorf("still have %d webhooks after unregister", len(d.webhooks))
	}
}
