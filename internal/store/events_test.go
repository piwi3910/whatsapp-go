package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

func TestInsertAndGetEvent(t *testing.T) {
	s := newTestStore(t)

	evt := &models.Event{
		Type:      models.EventMessageReceived,
		Payload:   json.RawMessage(`{"id":"msg-1"}`),
		Timestamp: 1000,
	}

	if err := s.InsertEvent(evt); err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}

	if evt.ID == 0 {
		t.Error("expected ID to be set after insert")
	}

	evts, err := s.GetEvents(0, 10)
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}
	if len(evts) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evts))
	}
	if evts[0].Type != evt.Type {
		t.Errorf("Type: got %q want %q", evts[0].Type, evt.Type)
	}
	if string(evts[0].Payload) != string(evt.Payload) {
		t.Errorf("Payload: got %q want %q", evts[0].Payload, evt.Payload)
	}
}

func TestGetEvents_CursorPagination(t *testing.T) {
	s := newTestStore(t)

	for i := 1; i <= 5; i++ {
		evt := &models.Event{
			Type:      models.EventMessageReceived,
			Payload:   json.RawMessage(fmt.Sprintf(`{"id":"msg-%d"}`, i)),
			Timestamp: int64(i * 100),
		}
		if err := s.InsertEvent(evt); err != nil {
			t.Fatalf("InsertEvent %d: %v", i, err)
		}
	}

	// Get first 3
	page1, err := s.GetEvents(0, 3)
	if err != nil {
		t.Fatalf("GetEvents page1: %v", err)
	}
	if len(page1) != 3 {
		t.Fatalf("expected 3 events, got %d", len(page1))
	}

	// Get next using last ID as cursor
	lastID := page1[len(page1)-1].ID
	page2, err := s.GetEvents(lastID, 10)
	if err != nil {
		t.Fatalf("GetEvents page2: %v", err)
	}
	if len(page2) != 2 {
		t.Fatalf("expected 2 events, got %d", len(page2))
	}

	// Verify order is ascending
	if page1[0].ID > page1[1].ID {
		t.Error("events not in ascending order")
	}
}

func TestGetEvents_EmptyPageIsNotNullJSON(t *testing.T) {
	s := newTestStore(t)

	evts, err := s.GetEvents(0, 10)
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}
	b, err := json.Marshal(evts)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(b) != "[]" {
		t.Errorf("empty event page marshalled as %s, want []", b)
	}
}

func TestPruneEvents(t *testing.T) {
	s := newTestStore(t)

	for i := 1; i <= 20; i++ {
		evt := &models.Event{
			Type:      models.EventMessageReceived,
			Payload:   json.RawMessage(fmt.Sprintf(`{"id":"msg-%d"}`, i)),
			Timestamp: int64(i * 100),
		}
		if err := s.InsertEvent(evt); err != nil {
			t.Fatalf("InsertEvent %d: %v", i, err)
		}
	}

	if err := s.PruneEvents(10); err != nil {
		t.Fatalf("PruneEvents: %v", err)
	}

	evts, err := s.GetEvents(0, 100)
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}
	if len(evts) != 10 {
		t.Fatalf("expected 10 events after prune, got %d", len(evts))
	}
}

// A consumer that falls further behind than the retention buffer must be
// told it lost events, not silently handed rows from past the deleted
// range. Without the watermark this returns events 11..20 with no error.
func TestGetEvents_GapAfterPrune(t *testing.T) {
	s := newTestStore(t)

	for i := 1; i <= 20; i++ {
		evt := &models.Event{
			Type:      models.EventMessageReceived,
			Payload:   json.RawMessage(fmt.Sprintf(`{"id":"msg-%d"}`, i)),
			Timestamp: int64(i * 100),
		}
		if err := s.InsertEvent(evt); err != nil {
			t.Fatalf("InsertEvent %d: %v", i, err)
		}
	}

	// The consumer has processed through event 3 and is about to poll again.
	if err := s.PruneEvents(10); err != nil {
		t.Fatalf("PruneEvents: %v", err)
	}

	evts, err := s.GetEvents(3, 100)
	if err == nil {
		t.Fatalf("expected a gap error, got %d events and nil error", len(evts))
	}
	if !errors.Is(err, ErrEventGap) {
		t.Fatalf("expected ErrEventGap, got %v", err)
	}
	var gap *GapError
	if !errors.As(err, &gap) {
		t.Fatalf("expected *GapError, got %T", err)
	}
	if gap.After != 3 {
		t.Errorf("GapError.After: got %d want 3", gap.After)
	}
	if gap.PrunedThrough != 10 {
		t.Errorf("GapError.PrunedThrough: got %d want 10", gap.PrunedThrough)
	}
	if evts != nil {
		t.Errorf("expected no events alongside the gap error, got %d", len(evts))
	}

	// Resuming from the reported safe cursor must succeed and return the
	// full retained tail.
	resumed, err := s.GetEvents(gap.PrunedThrough, 100)
	if err != nil {
		t.Fatalf("GetEvents from safe cursor: %v", err)
	}
	if len(resumed) != 10 {
		t.Errorf("expected 10 events from safe cursor, got %d", len(resumed))
	}
}

func TestGetEvents_NoGapForCaughtUpOrFreshConsumers(t *testing.T) {
	s := newTestStore(t)

	for i := 1; i <= 20; i++ {
		evt := &models.Event{
			Type:      models.EventMessageReceived,
			Payload:   json.RawMessage(fmt.Sprintf(`{"id":"msg-%d"}`, i)),
			Timestamp: int64(i * 100),
		}
		if err := s.InsertEvent(evt); err != nil {
			t.Fatalf("InsertEvent %d: %v", i, err)
		}
	}
	if err := s.PruneEvents(10); err != nil {
		t.Fatalf("PruneEvents: %v", err)
	}

	// Exactly at the watermark: nothing was missed.
	if _, err := s.GetEvents(10, 100); err != nil {
		t.Errorf("cursor at watermark should not be a gap: %v", err)
	}
	// Caught up past it.
	if _, err := s.GetEvents(15, 100); err != nil {
		t.Errorf("cursor past watermark should not be a gap: %v", err)
	}
	// A fresh consumer starting at 0 claims to have processed nothing, so
	// it just starts at the oldest retained event.
	evts, err := s.GetEvents(0, 100)
	if err != nil {
		t.Fatalf("after=0 should not be a gap: %v", err)
	}
	if len(evts) != 10 {
		t.Errorf("expected 10 events from 0, got %d", len(evts))
	}
}

func TestPruneEvents_WatermarkIsMonotonic(t *testing.T) {
	s := newTestStore(t)

	for i := 0; i < 20; i++ {
		evt := &models.Event{Type: models.EventConnectionConnected, Payload: json.RawMessage(`{}`), Timestamp: 1}
		if err := s.InsertEvent(evt); err != nil {
			t.Fatalf("InsertEvent: %v", err)
		}
	}
	if err := s.PruneEvents(5); err != nil {
		t.Fatalf("PruneEvents: %v", err)
	}
	first, err := s.PrunedThroughEventID()
	if err != nil {
		t.Fatalf("PrunedThroughEventID: %v", err)
	}
	if first != 15 {
		t.Fatalf("watermark: got %d want 15", first)
	}

	// Pruning with a buffer larger than the row count deletes nothing and
	// must not move the watermark backwards.
	if err := s.PruneEvents(1000); err != nil {
		t.Fatalf("PruneEvents: %v", err)
	}
	again, err := s.PrunedThroughEventID()
	if err != nil {
		t.Fatalf("PrunedThroughEventID: %v", err)
	}
	if again != first {
		t.Errorf("watermark moved backwards: got %d want %d", again, first)
	}
}

func TestPruneEvents_RejectsNonPositiveMax(t *testing.T) {
	s := newTestStore(t)
	if err := s.PruneEvents(0); err == nil {
		t.Error("expected an error for maxEvents=0")
	}
}

// whatsmeow redelivers messages after a reconnect or history sync. The same
// WhatsApp message must not produce a second message.received row, or an
// agent polling the cursor API replies twice.
func TestInsertEvent_DedupesRedeliveredMessage(t *testing.T) {
	s := newTestStore(t)

	payload := `{"id":"composite-abc","chat_jid":"chat@s.whatsapp.net","content":"hi"}`

	first := &models.Event{Type: models.EventMessageReceived, Payload: json.RawMessage(payload), Timestamp: 1000}
	inserted, err := s.InsertEventUnique(first)
	if err != nil {
		t.Fatalf("InsertEventUnique: %v", err)
	}
	if !inserted {
		t.Fatal("expected the first delivery to be inserted")
	}

	// Redelivery: same message, new event struct, later observed timestamp.
	second := &models.Event{Type: models.EventMessageReceived, Payload: json.RawMessage(payload), Timestamp: 2000}
	inserted, err = s.InsertEventUnique(second)
	if err != nil {
		t.Fatalf("InsertEventUnique redelivery: %v", err)
	}
	if inserted {
		t.Error("expected the redelivery to be suppressed")
	}
	if second.ID != first.ID {
		t.Errorf("duplicate should report the existing row ID: got %d want %d", second.ID, first.ID)
	}

	evts, err := s.GetEvents(0, 100)
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}
	if len(evts) != 1 {
		t.Fatalf("expected 1 event after redelivery, got %d", len(evts))
	}
	if evts[0].DedupeKey != models.EventMessageReceived+":composite-abc" {
		t.Errorf("DedupeKey: got %q", evts[0].DedupeKey)
	}
}

func TestInsertEvent_DistinctMessagesAndTypesCoexist(t *testing.T) {
	s := newTestStore(t)

	cases := []*models.Event{
		{Type: models.EventMessageReceived, Payload: json.RawMessage(`{"id":"a"}`), Timestamp: 1},
		{Type: models.EventMessageReceived, Payload: json.RawMessage(`{"id":"b"}`), Timestamp: 2},
		{Type: models.EventMessageSent, Payload: json.RawMessage(`{"id":"a"}`), Timestamp: 3},
		{Type: models.EventMessageReaction, Payload: json.RawMessage(`{"id":"a"}`), Timestamp: 4},
	}
	for i, evt := range cases {
		inserted, err := s.InsertEventUnique(evt)
		if err != nil {
			t.Fatalf("InsertEventUnique %d: %v", i, err)
		}
		if !inserted {
			t.Fatalf("event %d was wrongly suppressed", i)
		}
	}
	evts, err := s.GetEvents(0, 100)
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}
	if len(evts) != len(cases) {
		t.Fatalf("expected %d events, got %d", len(cases), len(evts))
	}
}

// Events with no natural key (connection changes, presence, receipts) are
// each a genuinely new fact and must never be collapsed.
func TestInsertEvent_KeylessEventsAreNeverSuppressed(t *testing.T) {
	s := newTestStore(t)

	for i := 0; i < 3; i++ {
		evt := &models.Event{Type: models.EventConnectionConnected, Payload: json.RawMessage(`{}`), Timestamp: int64(i)}
		inserted, err := s.InsertEventUnique(evt)
		if err != nil {
			t.Fatalf("InsertEventUnique %d: %v", i, err)
		}
		if !inserted {
			t.Fatalf("keyless event %d was suppressed", i)
		}
	}
	// Same message ID, but a read receipt carries no message-identity key.
	for i := 0; i < 2; i++ {
		evt := &models.Event{Type: models.EventMessageRead, Payload: json.RawMessage(`{"id":"a"}`), Timestamp: int64(i)}
		if _, err := s.InsertEventUnique(evt); err != nil {
			t.Fatalf("InsertEventUnique read %d: %v", i, err)
		}
	}

	evts, err := s.GetEvents(0, 100)
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}
	if len(evts) != 5 {
		t.Fatalf("expected 5 events, got %d", len(evts))
	}
	for _, e := range evts {
		if e.DedupeKey != "" {
			t.Errorf("event %d should have no dedupe key, got %q", e.ID, e.DedupeKey)
		}
	}
}

func TestInsertEvent_ExplicitDedupeKeyWins(t *testing.T) {
	s := newTestStore(t)

	a := &models.Event{Type: models.EventConnectionConnected, Payload: json.RawMessage(`{}`), Timestamp: 1, DedupeKey: "boot:1"}
	if _, err := s.InsertEventUnique(a); err != nil {
		t.Fatalf("InsertEventUnique: %v", err)
	}
	b := &models.Event{Type: models.EventConnectionConnected, Payload: json.RawMessage(`{}`), Timestamp: 2, DedupeKey: "boot:1"}
	inserted, err := s.InsertEventUnique(b)
	if err != nil {
		t.Fatalf("InsertEventUnique: %v", err)
	}
	if inserted {
		t.Error("expected the explicit duplicate key to suppress the insert")
	}
	if b.ID != a.ID {
		t.Errorf("ID: got %d want %d", b.ID, a.ID)
	}
}

func TestInsertEvent_MalformedPayloadGetsNoKey(t *testing.T) {
	s := newTestStore(t)

	for i := 0; i < 2; i++ {
		evt := &models.Event{Type: models.EventMessageReceived, Payload: json.RawMessage(`not json`), Timestamp: int64(i)}
		inserted, err := s.InsertEventUnique(evt)
		if err != nil {
			t.Fatalf("InsertEventUnique %d: %v", i, err)
		}
		if !inserted {
			t.Fatalf("keyless event %d was suppressed", i)
		}
	}
}

// Existing databases must migrate without losing rows, and the new UNIQUE
// index must survive duplicate events that predate it.
func TestMigrate_LegacyDatabaseWithDuplicateEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := db.Exec(`
CREATE TABLE events (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	type       TEXT NOT NULL,
	payload    TEXT NOT NULL,
	timestamp  INTEGER NOT NULL,
	created_at INTEGER NOT NULL DEFAULT (unixepoch())
);
INSERT INTO events (type, payload, timestamp) VALUES
	('message.received', '{"id":"dup"}', 1),
	('message.received', '{"id":"dup"}', 2),
	('message.received', '{"id":"dup"}', 3),
	('message.received', '{"id":"unique"}', 4),
	('connection.connected', '{}', 5),
	('message.received', 'not json', 6);
`); err != nil {
		t.Fatalf("seeding legacy db: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s, err := New(path)
	if err != nil {
		t.Fatalf("New on legacy db: %v", err)
	}
	defer s.Close()

	evts, err := s.GetEvents(0, 100)
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}
	if len(evts) != 6 {
		t.Fatalf("migration lost rows: got %d want 6", len(evts))
	}

	// The first occurrence of the duplicate group keeps the key; the later
	// copies stay NULL so the UNIQUE index can be created.
	byID := map[int64]models.Event{}
	for _, e := range evts {
		byID[e.ID] = e
	}
	if got, want := byID[1].DedupeKey, "message.received:dup"; got != want {
		t.Errorf("row 1 DedupeKey: got %q want %q", got, want)
	}
	for _, id := range []int64{2, 3, 5, 6} {
		if byID[id].DedupeKey != "" {
			t.Errorf("row %d should have no dedupe key, got %q", id, byID[id].DedupeKey)
		}
	}
	if got, want := byID[4].DedupeKey, "message.received:unique"; got != want {
		t.Errorf("row 4 DedupeKey: got %q want %q", got, want)
	}

	// The index exists and is enforced from here on.
	evt := &models.Event{Type: models.EventMessageReceived, Payload: json.RawMessage(`{"id":"dup"}`), Timestamp: 7}
	inserted, err := s.InsertEventUnique(evt)
	if err != nil {
		t.Fatalf("InsertEventUnique: %v", err)
	}
	if inserted {
		t.Error("redelivery of a backfilled message should be suppressed")
	}

	// Re-opening (migration runs again) must be a no-op.
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	s2, err := New(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	evts2, err := s2.GetEvents(0, 100)
	if err != nil {
		t.Fatalf("GetEvents after reopen: %v", err)
	}
	if len(evts2) != 6 {
		t.Errorf("second migration changed row count: got %d want 6", len(evts2))
	}
}
