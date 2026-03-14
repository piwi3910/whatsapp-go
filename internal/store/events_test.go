package store

import (
	"testing"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

func TestInsertAndGetEvent(t *testing.T) {
	s := newTestStore(t)

	evt := &models.Event{
		Type:      models.EventMessageReceived,
		Payload:   `{"id":"msg-1"}`,
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
	if evts[0].Payload != evt.Payload {
		t.Errorf("Payload: got %q want %q", evts[0].Payload, evt.Payload)
	}
}

func TestGetEvents_CursorPagination(t *testing.T) {
	s := newTestStore(t)

	for i := 1; i <= 5; i++ {
		evt := &models.Event{
			Type:      models.EventMessageReceived,
			Payload:   `{}`,
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

func TestPruneEvents(t *testing.T) {
	s := newTestStore(t)

	for i := 1; i <= 20; i++ {
		evt := &models.Event{
			Type:      models.EventMessageReceived,
			Payload:   `{}`,
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
