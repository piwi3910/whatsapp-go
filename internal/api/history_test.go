package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// Issue #21: the history-sync endpoint validates input and forwards to the
// client. A small wrapper adds a SyncHistory override to the shared stub,
// the same pattern as the per-test overrides elsewhere.

type historyStub struct {
	stubService
	fn func(ctx context.Context, chatJID string, count int) (int, error)
}

func (s *historyStub) SyncHistory(ctx context.Context, chatJID string, count int) (int, error) {
	if s.fn != nil {
		return s.fn(ctx, chatJID, count)
	}
	return 0, nil
}

func TestHistorySyncValidatesInput(t *testing.T) {
	s := testServer(t, &historyStub{stubService: stubService{}})

	// Missing chat_jid.
	rec := do(s, authRequest(http.MethodPost, "/api/v1/history/sync", []byte(`{"count": 50}`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing chat_jid: status = %d, want 400", rec.Code)
	}

	// Unparseable JID.
	rec = do(s, authRequest(http.MethodPost, "/api/v1/history/sync", []byte(`{"chat_jid": "not a jid at all"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad jid: status = %d, want 400", rec.Code)
	}
}

func TestHistorySyncForwardsToClient(t *testing.T) {
	var gotChat string
	var gotCount int
	svc := &historyStub{
		fn: func(_ context.Context, chatJID string, count int) (int, error) {
			gotChat, gotCount = chatJID, count
			return 7, nil
		},
	}
	s := testServer(t, svc)

	rec := do(s, authRequest(http.MethodPost, "/api/v1/history/sync", []byte(`{"chat_jid": "+1234567890", "count": 50}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if gotChat != "+1234567890" || gotCount != 50 {
		t.Errorf("client got (%q, %d), want (\"+1234567890\", 50)", gotChat, gotCount)
	}

	var body struct {
		OK   bool           `json:"ok"`
		Data map[string]any `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if !body.OK || body.Data["imported"] != float64(7) {
		t.Errorf("body = %+v, want ok with imported=7", body.Data)
	}
}
