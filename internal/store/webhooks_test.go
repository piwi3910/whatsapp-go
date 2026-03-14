package store

import (
	"testing"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

func TestWebhookCRUD(t *testing.T) {
	s := newTestStore(t)

	wh := &models.Webhook{
		ID:     "wh-1",
		URL:    "https://example.com/webhook",
		Events: []string{models.EventMessageReceived, models.EventMessageSent},
		Secret: "mysecret",
	}

	// Insert
	if err := s.InsertWebhook(wh); err != nil {
		t.Fatalf("InsertWebhook: %v", err)
	}

	// List
	whs, err := s.GetWebhooks()
	if err != nil {
		t.Fatalf("GetWebhooks: %v", err)
	}
	if len(whs) != 1 {
		t.Fatalf("expected 1 webhook, got %d", len(whs))
	}
	if whs[0].ID != wh.ID {
		t.Errorf("ID: got %q want %q", whs[0].ID, wh.ID)
	}
	if whs[0].URL != wh.URL {
		t.Errorf("URL: got %q want %q", whs[0].URL, wh.URL)
	}
	if whs[0].Secret != wh.Secret {
		t.Errorf("Secret: got %q want %q", whs[0].Secret, wh.Secret)
	}
	if len(whs[0].Events) != len(wh.Events) {
		t.Errorf("Events length: got %d want %d", len(whs[0].Events), len(wh.Events))
	}

	// Delete
	if err := s.DeleteWebhook("wh-1"); err != nil {
		t.Fatalf("DeleteWebhook: %v", err)
	}

	// Verify empty
	whs, err = s.GetWebhooks()
	if err != nil {
		t.Fatalf("GetWebhooks after delete: %v", err)
	}
	if len(whs) != 0 {
		t.Errorf("expected 0 webhooks after delete, got %d", len(whs))
	}

	// Delete nonexistent
	if err := s.DeleteWebhook("nonexistent"); err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestWebhook_NoSecret(t *testing.T) {
	s := newTestStore(t)

	wh := &models.Webhook{
		ID:     "wh-nosecret",
		URL:    "https://example.com/webhook",
		Events: []string{models.EventMessageReceived},
	}

	if err := s.InsertWebhook(wh); err != nil {
		t.Fatalf("InsertWebhook: %v", err)
	}

	whs, err := s.GetWebhooks()
	if err != nil {
		t.Fatalf("GetWebhooks: %v", err)
	}
	if len(whs) != 1 {
		t.Fatalf("expected 1 webhook, got %d", len(whs))
	}
	if whs[0].Secret != "" {
		t.Errorf("expected empty secret, got %q", whs[0].Secret)
	}
}
