package store

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

var ErrNotFound = errors.New("not found")

func (s *Store) InsertMessage(msg *models.Message) error {
	_, err := s.db.Exec(`
INSERT INTO messages
	(id, chat_jid, sender_jid, wa_id, type, content, media_type, media_size,
	 media_url, media_key, caption, timestamp, is_from_me, is_read, raw_proto)
VALUES
	(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
-- Two conflict targets, not one. The primary key is a 64-bit hash of
-- (chat_jid, sender_jid, wa_id), which is also covered by the UNIQUE index
-- idx_messages_identity, so in practice a re-delivery collides on both at
-- once. They can come apart exactly once: a hash collision between two
-- different messages hits idx_messages_identity without hitting the id, and
-- with only the id clause that surfaced as an unhandled UNIQUE constraint
-- violation — i.e. a dropped inbound message. Handling both makes the
-- redundancy harmless instead of a latent failure. The index is kept rather
-- than dropped because it is what enforces message identity independently of
-- the hash.
ON CONFLICT(id) DO UPDATE SET
	content = excluded.content,
	is_read = excluded.is_read
ON CONFLICT(chat_jid, sender_jid, wa_id) DO UPDATE SET
	content = excluded.content,
	is_read = excluded.is_read`,
		msg.ID, msg.ChatJID, msg.SenderJID, msg.WaID, msg.Type,
		nullString(msg.Content), nullString(msg.MediaType), nullInt64(msg.MediaSize),
		nullString(msg.MediaURL), msg.MediaKey,
		nullString(msg.Caption), msg.Timestamp,
		boolToInt(msg.IsFromMe), boolToInt(msg.IsRead), msg.RawProto,
	)
	if err != nil {
		return fmt.Errorf("inserting message: %w", err)
	}
	return nil
}

func (s *Store) GetMessage(id string) (*models.Message, error) {
	row := s.db.QueryRow(`
SELECT id, chat_jid, sender_jid, wa_id, type,
	COALESCE(content, ''), COALESCE(media_type, ''), COALESCE(media_size, 0),
	COALESCE(media_url, ''), media_key, COALESCE(caption, ''),
	timestamp, is_from_me, is_read, raw_proto, created_at
FROM messages WHERE id = ?`, id)

	var msg models.Message
	var isFromMe, isRead int
	err := row.Scan(
		&msg.ID, &msg.ChatJID, &msg.SenderJID, &msg.WaID, &msg.Type,
		&msg.Content, &msg.MediaType, &msg.MediaSize,
		&msg.MediaURL, &msg.MediaKey, &msg.Caption,
		&msg.Timestamp, &isFromMe, &isRead, &msg.RawProto, &msg.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("getting message: %w", err)
	}
	msg.IsFromMe = isFromMe != 0
	msg.IsRead = isRead != 0
	return &msg, nil
}

// MessageCursor is a keyset pagination cursor: the (timestamp, id) tuple of
// the last row of the previous page.
//
// Timestamps have second granularity, so a timestamp alone is not a unique
// position — several messages in a busy chat routinely share one second.
// Paging on the timestamp alone skipped every message that shared the last
// row's second. The ID breaks the tie: rows are totally ordered by
// (timestamp DESC, id DESC) and the cursor names an exact row.
type MessageCursor struct {
	Timestamp int64
	// ID is the message ID of the last row returned. When empty, the
	// cursor degrades to the legacy "strictly older than Timestamp"
	// behaviour, which can still skip rows sharing that second.
	ID string
}

// GetMessages returns up to limit messages for a chat, newest first,
// strictly older than the before timestamp (0 for the newest page).
//
// Prefer GetMessagesPage: this timestamp-only cursor cannot address rows
// individually and drops messages that share the boundary second.
func (s *Store) GetMessages(chatJID string, limit int, before int64) ([]models.Message, error) {
	var cur *MessageCursor
	if before > 0 {
		cur = &MessageCursor{Timestamp: before}
	}
	return s.GetMessagesPage(chatJID, limit, cur)
}

// GetMessagesPage returns up to limit messages for a chat ordered newest
// first, starting immediately after cur. Pass a nil cursor for the first
// page; pass the (Timestamp, ID) of the last row you received for the next
// one. No message is skipped or repeated at a page boundary, even when many
// messages share the same one-second timestamp.
func (s *Store) GetMessagesPage(chatJID string, limit int, cur *MessageCursor) ([]models.Message, error) {
	where := "chat_jid = ?"
	args := []any{chatJID}
	switch {
	case cur == nil || cur.Timestamp <= 0:
		// First page: no cursor predicate.
	case cur.ID == "":
		where += " AND timestamp < ?"
		args = append(args, cur.Timestamp)
	default:
		where += " AND (timestamp < ? OR (timestamp = ? AND id < ?))"
		args = append(args, cur.Timestamp, cur.Timestamp, cur.ID)
	}
	args = append(args, limit)

	query := `
SELECT id, chat_jid, sender_jid, wa_id, type,
	COALESCE(content, ''), COALESCE(media_type, ''), COALESCE(media_size, 0),
	COALESCE(caption, ''), timestamp, is_from_me, is_read, created_at
FROM messages
WHERE ` + where + `
ORDER BY timestamp DESC, id DESC
LIMIT ?`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying messages: %w", err)
	}
	defer rows.Close()

	// Non-nil so an empty page marshals as [] rather than null.
	msgs := make([]models.Message, 0)
	for rows.Next() {
		var msg models.Message
		var isFromMe, isRead int
		err := rows.Scan(
			&msg.ID, &msg.ChatJID, &msg.SenderJID, &msg.WaID, &msg.Type,
			&msg.Content, &msg.MediaType, &msg.MediaSize,
			&msg.Caption, &msg.Timestamp, &isFromMe, &isRead, &msg.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scanning message: %w", err)
		}
		msg.IsFromMe = isFromMe != 0
		msg.IsRead = isRead != 0
		msgs = append(msgs, msg)
	}
	return msgs, rows.Err()
}

func (s *Store) DeleteMessage(id string) error {
	res, err := s.db.Exec(`DELETE FROM messages WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting message: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) UpdateReadStatus(id string, read bool) error {
	_, err := s.db.Exec(`UPDATE messages SET is_read = ? WHERE id = ?`, boolToInt(read), id)
	if err != nil {
		return fmt.Errorf("updating read status: %w", err)
	}
	return nil
}

func nullString(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func nullInt64(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
