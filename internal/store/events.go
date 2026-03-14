package store

import (
	"fmt"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

func (s *Store) InsertEvent(evt *models.Event) error {
	res, err := s.db.Exec(`
INSERT INTO events (type, payload, timestamp)
VALUES (?, ?, ?)`,
		evt.Type, evt.Payload, evt.Timestamp,
	)
	if err != nil {
		return fmt.Errorf("inserting event: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("getting last insert id: %w", err)
	}
	evt.ID = id
	return nil
}

func (s *Store) GetEvents(after int64, limit int) ([]models.Event, error) {
	rows, err := s.db.Query(`
SELECT id, type, payload, timestamp
FROM events
WHERE id > ?
ORDER BY id ASC
LIMIT ?`, after, limit)
	if err != nil {
		return nil, fmt.Errorf("querying events: %w", err)
	}
	defer rows.Close()

	var evts []models.Event
	for rows.Next() {
		var evt models.Event
		if err := rows.Scan(&evt.ID, &evt.Type, &evt.Payload, &evt.Timestamp); err != nil {
			return nil, fmt.Errorf("scanning event: %w", err)
		}
		evts = append(evts, evt)
	}
	return evts, rows.Err()
}

func (s *Store) PruneEvents(maxEvents int) error {
	_, err := s.db.Exec(`
DELETE FROM events
WHERE id NOT IN (
	SELECT id FROM events ORDER BY id DESC LIMIT ?
)`, maxEvents)
	if err != nil {
		return fmt.Errorf("pruning events: %w", err)
	}
	return nil
}
