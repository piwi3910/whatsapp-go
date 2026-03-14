package store

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("newTestStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestInsertAndGetMessage(t *testing.T) {
	s := newTestStore(t)

	msg := &models.Message{
		ID:        "msg-1",
		ChatJID:   "chat@s.whatsapp.net",
		SenderJID: "sender@s.whatsapp.net",
		WaID:      "wa-1",
		Type:      "text",
		Content:   "hello world",
		Timestamp: 1000,
		IsFromMe:  true,
	}

	if err := s.InsertMessage(msg); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}

	got, err := s.GetMessage("msg-1")
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}

	if got.ID != msg.ID {
		t.Errorf("ID: got %q want %q", got.ID, msg.ID)
	}
	if got.Content != msg.Content {
		t.Errorf("Content: got %q want %q", got.Content, msg.Content)
	}
	if got.IsFromMe != msg.IsFromMe {
		t.Errorf("IsFromMe: got %v want %v", got.IsFromMe, msg.IsFromMe)
	}
}

func TestGetMessages_Pagination(t *testing.T) {
	s := newTestStore(t)

	for i := 1; i <= 5; i++ {
		msg := &models.Message{
			ID:        fmt.Sprintf("msg-%d", i),
			ChatJID:   "chat@s.whatsapp.net",
			SenderJID: "sender@s.whatsapp.net",
			WaID:      fmt.Sprintf("wa-%d", i),
			Type:      "text",
			Content:   fmt.Sprintf("message %d", i),
			Timestamp: int64(i * 100),
		}
		if err := s.InsertMessage(msg); err != nil {
			t.Fatalf("InsertMessage %d: %v", i, err)
		}
	}

	msgs, err := s.GetMessages("chat@s.whatsapp.net", 3, 0)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}

	// Should be ordered by timestamp DESC
	if msgs[0].Timestamp < msgs[1].Timestamp {
		t.Errorf("messages not in DESC order: %d < %d", msgs[0].Timestamp, msgs[1].Timestamp)
	}
	if msgs[1].Timestamp < msgs[2].Timestamp {
		t.Errorf("messages not in DESC order: %d < %d", msgs[1].Timestamp, msgs[2].Timestamp)
	}
}

func TestGetMessage_NotFound(t *testing.T) {
	s := newTestStore(t)

	_, err := s.GetMessage("nonexistent")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestDeleteMessage(t *testing.T) {
	s := newTestStore(t)

	msg := &models.Message{
		ID:        "msg-del",
		ChatJID:   "chat@s.whatsapp.net",
		SenderJID: "sender@s.whatsapp.net",
		WaID:      "wa-del",
		Type:      "text",
		Timestamp: 1000,
	}
	if err := s.InsertMessage(msg); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}

	if err := s.DeleteMessage("msg-del"); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}

	if err := s.DeleteMessage("msg-del"); err != ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestUpdateReadStatus(t *testing.T) {
	s := newTestStore(t)

	msg := &models.Message{
		ID:        "msg-read",
		ChatJID:   "chat@s.whatsapp.net",
		SenderJID: "sender@s.whatsapp.net",
		WaID:      "wa-read",
		Type:      "text",
		Timestamp: 1000,
		IsRead:    false,
	}
	if err := s.InsertMessage(msg); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}

	if err := s.UpdateReadStatus("msg-read", true); err != nil {
		t.Fatalf("UpdateReadStatus: %v", err)
	}

	got, err := s.GetMessage("msg-read")
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if !got.IsRead {
		t.Error("expected IsRead to be true")
	}
}
