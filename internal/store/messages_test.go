package store

import (
	"encoding/json"
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

// Second-granularity timestamps mean a busy chat routinely puts several
// messages in one second. Paging on the timestamp alone skipped every
// message sharing the boundary second; the keyset cursor must walk the
// whole history exactly once.
func TestGetMessagesPage_NoLossAtPageBoundary(t *testing.T) {
	s := newTestStore(t)

	const chat = "chat@s.whatsapp.net"
	const total = 20
	// 20 messages spread over 4 seconds: 5 messages share each timestamp,
	// so every page boundary at limit=3 lands mid-second.
	for i := 0; i < total; i++ {
		msg := &models.Message{
			ID:        fmt.Sprintf("msg-%02d", i),
			ChatJID:   chat,
			SenderJID: "sender@s.whatsapp.net",
			WaID:      fmt.Sprintf("wa-%02d", i),
			Type:      "text",
			Content:   fmt.Sprintf("message %d", i),
			Timestamp: int64(1000 + i/5),
		}
		if err := s.InsertMessage(msg); err != nil {
			t.Fatalf("InsertMessage %d: %v", i, err)
		}
	}

	seen := map[string]int{}
	var cur *MessageCursor
	for page := 0; page < 100; page++ {
		msgs, err := s.GetMessagesPage(chat, 3, cur)
		if err != nil {
			t.Fatalf("GetMessagesPage: %v", err)
		}
		if len(msgs) == 0 {
			break
		}
		for _, m := range msgs {
			seen[m.ID]++
		}
		last := msgs[len(msgs)-1]
		cur = &MessageCursor{Timestamp: last.Timestamp, ID: last.ID}
	}

	if len(seen) != total {
		t.Errorf("walked %d distinct messages, want %d", len(seen), total)
	}
	for i := 0; i < total; i++ {
		id := fmt.Sprintf("msg-%02d", i)
		switch seen[id] {
		case 1:
		case 0:
			t.Errorf("message %s was skipped at a page boundary", id)
		default:
			t.Errorf("message %s was returned %d times", id, seen[id])
		}
	}
}

func TestGetMessagesPage_OrderIsTotalAndStable(t *testing.T) {
	s := newTestStore(t)

	const chat = "chat@s.whatsapp.net"
	for i := 0; i < 6; i++ {
		msg := &models.Message{
			ID:        fmt.Sprintf("msg-%d", i),
			ChatJID:   chat,
			SenderJID: "sender@s.whatsapp.net",
			WaID:      fmt.Sprintf("wa-%d", i),
			Type:      "text",
			Timestamp: 1000, // all in the same second
		}
		if err := s.InsertMessage(msg); err != nil {
			t.Fatalf("InsertMessage %d: %v", i, err)
		}
	}

	all, err := s.GetMessagesPage(chat, 10, nil)
	if err != nil {
		t.Fatalf("GetMessagesPage: %v", err)
	}
	if len(all) != 6 {
		t.Fatalf("expected 6 messages, got %d", len(all))
	}
	for i := 1; i < len(all); i++ {
		if all[i-1].ID <= all[i].ID {
			t.Errorf("order not strictly descending by id: %q then %q", all[i-1].ID, all[i].ID)
		}
	}
}

func TestGetMessagesPage_OtherChatsExcluded(t *testing.T) {
	s := newTestStore(t)

	for i, chat := range []string{"a@s.whatsapp.net", "b@s.whatsapp.net"} {
		msg := &models.Message{
			ID:        fmt.Sprintf("msg-%d", i),
			ChatJID:   chat,
			SenderJID: "sender@s.whatsapp.net",
			WaID:      fmt.Sprintf("wa-%d", i),
			Type:      "text",
			Timestamp: 1000,
		}
		if err := s.InsertMessage(msg); err != nil {
			t.Fatalf("InsertMessage: %v", err)
		}
	}

	msgs, err := s.GetMessagesPage("a@s.whatsapp.net", 10, nil)
	if err != nil {
		t.Fatalf("GetMessagesPage: %v", err)
	}
	if len(msgs) != 1 || msgs[0].ChatJID != "a@s.whatsapp.net" {
		t.Errorf("expected only chat a's message, got %+v", msgs)
	}
}

func TestGetMessages_EmptyPageIsNotNullJSON(t *testing.T) {
	s := newTestStore(t)

	msgs, err := s.GetMessages("nobody@s.whatsapp.net", 10, 0)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	b, err := json.Marshal(msgs)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(b) != "[]" {
		t.Errorf("empty message page marshalled as %s, want []", b)
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

// TestInsertMessage_RedeliveryUpsert covers the path WhatsApp actually
// exercises on every reconnect: the same message arriving again. The upsert
// declares two ON CONFLICT targets — the primary key and the
// (chat_jid, sender_jid, wa_id) identity index — because the row id is a
// 64-bit hash of that same tuple, so a hash collision would otherwise
// surface as an unhandled UNIQUE violation instead of an update. Without
// both targets one of these three cases fails.
func TestInsertMessage_RedeliveryUpsert(t *testing.T) {
	s := newTestStore(t)

	base := &models.Message{
		ID: "id-1", ChatJID: "12345678901@s.whatsapp.net",
		SenderJID: "12345678901@s.whatsapp.net", WaID: "WA1",
		Type: "text", Content: "first", Timestamp: 100,
		RawProto: []byte("proto-v1"),
	}
	if err := s.InsertMessage(base); err != nil {
		t.Fatalf("initial insert: %v", err)
	}

	t.Run("same id and same identity", func(t *testing.T) {
		again := *base
		again.Content = "edited"
		again.IsRead = true
		if err := s.InsertMessage(&again); err != nil {
			t.Fatalf("redelivery must upsert, not fail: %v", err)
		}
		got, err := s.GetMessage("id-1")
		if err != nil {
			t.Fatal(err)
		}
		if got.Content != "edited" || !got.IsRead {
			t.Errorf("redelivery did not update the row: content=%q read=%v", got.Content, got.IsRead)
		}
		if string(got.RawProto) != "proto-v1" {
			t.Errorf("raw_proto = %q, want the original to survive a content-only echo", got.RawProto)
		}
	})

	t.Run("same identity, different id (hash collision)", func(t *testing.T) {
		collided := *base
		collided.ID = "id-collision"
		collided.Content = "same identity, other id"
		if err := s.InsertMessage(&collided); err != nil {
			t.Fatalf("identity conflict must upsert, not violate the unique index: %v", err)
		}
		// The identity is what matters: still exactly one row for it.
		msgs, err := s.GetMessagesPage(base.ChatJID, 10, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) != 1 {
			t.Fatalf("rows for one message identity = %d, want 1", len(msgs))
		}
	})

	t.Run("different identity inserts a new row", func(t *testing.T) {
		other := *base
		other.ID = "id-2"
		other.WaID = "WA2"
		other.Content = "second"
		other.Timestamp = 101
		if err := s.InsertMessage(&other); err != nil {
			t.Fatal(err)
		}
		msgs, err := s.GetMessagesPage(base.ChatJID, 10, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) != 2 {
			t.Fatalf("rows = %d, want 2 distinct messages", len(msgs))
		}
	})
}
