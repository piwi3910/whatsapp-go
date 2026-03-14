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
ON CONFLICT(id) DO UPDATE SET
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

func (s *Store) GetMessages(chatJID string, limit int, before int64) ([]models.Message, error) {
	query := `
SELECT id, chat_jid, sender_jid, wa_id, type,
	COALESCE(content, ''), COALESCE(media_type, ''), COALESCE(media_size, 0),
	COALESCE(caption, ''), timestamp, is_from_me, is_read, created_at
FROM messages
WHERE chat_jid = ? AND (? = 0 OR timestamp < ?)
ORDER BY timestamp DESC
LIMIT ?`

	rows, err := s.db.Query(query, chatJID, before, before, limit)
	if err != nil {
		return nil, fmt.Errorf("querying messages: %w", err)
	}
	defer rows.Close()

	var msgs []models.Message
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

func nullString(v string) interface{} {
	if v == "" {
		return nil
	}
	return v
}

func nullInt64(v int64) interface{} {
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
