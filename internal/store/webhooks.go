package store

import (
	"encoding/json"
	"fmt"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

func (s *Store) InsertWebhook(wh *models.Webhook) error {
	eventsJSON, err := json.Marshal(wh.Events)
	if err != nil {
		return fmt.Errorf("marshaling events: %w", err)
	}
	_, err = s.db.Exec(`
INSERT INTO webhooks (id, url, events, secret)
VALUES (?, ?, ?, ?)`,
		wh.ID, wh.URL, string(eventsJSON), nullString(wh.Secret),
	)
	if err != nil {
		return fmt.Errorf("inserting webhook: %w", err)
	}
	return nil
}

func (s *Store) GetWebhooks() ([]models.Webhook, error) {
	rows, err := s.db.Query(`
SELECT id, url, events, COALESCE(secret, ''), created_at
FROM webhooks`)
	if err != nil {
		return nil, fmt.Errorf("querying webhooks: %w", err)
	}
	defer rows.Close()

	var whs []models.Webhook
	for rows.Next() {
		var wh models.Webhook
		var eventsJSON string
		if err := rows.Scan(&wh.ID, &wh.URL, &eventsJSON, &wh.Secret, &wh.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning webhook: %w", err)
		}
		if err := json.Unmarshal([]byte(eventsJSON), &wh.Events); err != nil {
			return nil, fmt.Errorf("unmarshaling events: %w", err)
		}
		whs = append(whs, wh)
	}
	return whs, rows.Err()
}

func (s *Store) DeleteWebhook(id string) error {
	res, err := s.db.Exec(`DELETE FROM webhooks WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting webhook: %w", err)
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
