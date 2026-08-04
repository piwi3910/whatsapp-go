package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

// metaEventsPrunedThrough is the meta key holding the highest event ID that
// PruneEvents has ever deleted. Everything above it is still retained;
// everything at or below it is gone for good.
const metaEventsPrunedThrough = "events_pruned_through"

// ErrEventGap is the sentinel for "the cursor you polled with points into a
// range that has already been pruned". Match it with errors.Is; use
// errors.As with *GapError to recover the safe cursor to resume from.
var ErrEventGap = errors.New("event log gap: requested cursor is older than the retained log")

// GapError reports that events between the caller's cursor and
// PrunedThrough were deleted by retention before the caller consumed them.
//
// The caller has provably missed events. It cannot recover them from the
// event log — it must reconcile from the messages table (which is keyed by
// message identity and is not pruned) and then resume polling from
// PrunedThrough.
type GapError struct {
	// After is the cursor the caller polled with.
	After int64
	// PrunedThrough is the highest event ID that has been deleted. It is
	// also the oldest safe cursor: polling with after=PrunedThrough
	// returns the first still-retained event.
	PrunedThrough int64
}

func (e *GapError) Error() string {
	return fmt.Sprintf("event log gap: cursor %d is older than the pruned watermark %d; events %d..%d were deleted before they were consumed",
		e.After, e.PrunedThrough, e.After+1, e.PrunedThrough)
}

func (e *GapError) Unwrap() error { return ErrEventGap }

// messageEventTypes are the event types whose payload is a marshalled
// models.Message, and therefore carries a natural idempotency key: the
// composite message ID (see internal/jid.CompositeMessageID).
var messageEventTypes = map[string]bool{
	models.EventMessageReceived: true,
	models.EventMessageSent:     true,
	models.EventMessageReaction: true,
}

// DedupeKeyFor returns the natural idempotency key for an event, or "" if
// the event has no natural key (connection changes, presence, receipts —
// each occurrence of those is genuinely a new fact).
//
// For message events the key is "<event type>:<composite message id>". The
// composite ID is derived from (chat JID, sender JID, WhatsApp message ID),
// so whatsmeow redelivering the same message after a reconnect or a history
// sync produces the same key and is collapsed into the row already stored.
func DedupeKeyFor(evt *models.Event) string {
	if !messageEventTypes[evt.Type] {
		return ""
	}
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(evt.Payload, &p); err != nil || p.ID == "" {
		return ""
	}
	return evt.Type + ":" + p.ID
}

// InsertEvent appends an event to the durable log, suppressing redeliveries
// of an event that is already stored (see InsertEventUnique). evt.ID is set
// either to the new row's ID or to the ID of the row that already carried
// the same dedupe key.
func (s *Store) InsertEvent(evt *models.Event) error {
	_, err := s.InsertEventUnique(evt)
	return err
}

// InsertEventUnique appends an event to the durable log and reports whether
// it was actually inserted (true) or suppressed as a duplicate (false).
//
// Delivery from WhatsApp is at-least-once: whatsmeow redelivers messages
// after a reconnect or during a history sync. Without a key, one WhatsApp
// message would produce several `message.received` rows and an agent
// polling the cursor API would reply more than once. The dedupe key makes
// the insert idempotent — the second delivery is dropped and evt.ID is set
// to the ID of the row already in the log.
//
// If evt.DedupeKey is empty it is derived with DedupeKeyFor. Events with no
// natural key are stored with a NULL key; SQLite permits any number of
// NULLs in a UNIQUE index, so those are never suppressed.
//
// Consumers should still treat the feed as at-least-once end to end (a
// consumer can crash between processing an event and persisting its
// cursor) and use the message ID in the payload as their idempotency key.
func (s *Store) InsertEventUnique(evt *models.Event) (bool, error) {
	if evt.DedupeKey == "" {
		evt.DedupeKey = DedupeKeyFor(evt)
	}

	res, err := s.db.Exec(`
INSERT INTO events (type, payload, timestamp, dedupe_key)
VALUES (?, ?, ?, ?)
ON CONFLICT(dedupe_key) DO NOTHING`,
		evt.Type, evt.Payload, evt.Timestamp, nullString(evt.DedupeKey),
	)
	if err != nil {
		return false, fmt.Errorf("inserting event: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("checking rows affected: %w", err)
	}
	if n == 0 {
		// Suppressed redelivery. Point the caller at the row that won so
		// it can still report a meaningful ID.
		var existing int64
		err := s.db.QueryRow(`SELECT id FROM events WHERE dedupe_key = ?`, evt.DedupeKey).Scan(&existing)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, fmt.Errorf("looking up duplicate event: %w", err)
		}
		evt.ID = existing
		return false, nil
	}
	id, err := res.LastInsertId()
	if err != nil {
		return false, fmt.Errorf("getting last insert id: %w", err)
	}
	evt.ID = id
	return true, nil
}

// GetEvents returns up to limit events with ID greater than after, oldest
// first. Passing after=0 starts from the oldest retained event.
//
// If after is positive but points below the pruned watermark, the events
// between it and the watermark have been deleted by retention and GetEvents
// returns a *GapError (matching errors.Is(err, ErrEventGap)) instead of
// silently returning rows from past the hole. A consumer that gets this has
// missed events and must reconcile from the messages table before resuming
// from GapError.PrunedThrough.
//
// after=0 is deliberately never a gap: a caller starting from zero is not
// claiming to have consumed anything, so it just starts at the oldest
// retained event.
func (s *Store) GetEvents(after int64, limit int) ([]models.Event, error) {
	if after > 0 {
		prunedThrough, err := s.PrunedThroughEventID()
		if err != nil {
			return nil, err
		}
		if after < prunedThrough {
			return nil, &GapError{After: after, PrunedThrough: prunedThrough}
		}
	}

	rows, err := s.db.Query(`
SELECT id, type, payload, timestamp, COALESCE(dedupe_key, '')
FROM events
WHERE id > ?
ORDER BY id ASC
LIMIT ?`, after, limit)
	if err != nil {
		return nil, fmt.Errorf("querying events: %w", err)
	}
	defer rows.Close()

	// Non-nil so an empty page marshals as [] rather than null.
	evts := make([]models.Event, 0)
	for rows.Next() {
		var evt models.Event
		// Scan the payload through a string rather than straight into
		// json.RawMessage: the column is TEXT, and database/sql refuses to
		// store a driver string into a []byte-kinded named type. Rows
		// written before the column carried JSON bytes come back as strings
		// too, so this also keeps legacy data readable.
		var payload string
		if err := rows.Scan(&evt.ID, &evt.Type, &payload, &evt.Timestamp, &evt.DedupeKey); err != nil {
			return nil, fmt.Errorf("scanning event: %w", err)
		}
		evt.Payload = json.RawMessage(payload)
		evts = append(evts, evt)
	}
	return evts, rows.Err()
}

// PrunedThroughEventID returns the highest event ID that retention has
// deleted, or 0 if nothing has ever been pruned. It is the oldest cursor a
// consumer can poll with and still be guaranteed a gap-free stream.
func (s *Store) PrunedThroughEventID() (int64, error) {
	return s.getMetaInt64(metaEventsPrunedThrough)
}

// PruneEvents keeps only the newest maxEvents rows in the log and records
// the highest ID it deleted as the pruned watermark, so that a consumer
// left behind by retention is told it lost events (see GetEvents) instead
// of silently resuming past the hole.
func (s *Store) PruneEvents(maxEvents int) error {
	if maxEvents <= 0 {
		return fmt.Errorf("pruning events: maxEvents must be positive, got %d", maxEvents)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("pruning events: %w", err)
	}
	defer tx.Rollback()

	// Record the watermark and delete in one transaction: a crash between
	// the two would otherwise leave a silent gap, which is exactly the
	// failure this guards against.
	var highestDeleted int64
	err = tx.QueryRow(`
SELECT COALESCE(MAX(id), 0) FROM events
WHERE id NOT IN (SELECT id FROM events ORDER BY id DESC LIMIT ?)`, maxEvents).Scan(&highestDeleted)
	if err != nil {
		return fmt.Errorf("finding prune watermark: %w", err)
	}
	if highestDeleted == 0 {
		return tx.Commit()
	}

	if _, err := tx.Exec(`DELETE FROM events WHERE id <= ?`, highestDeleted); err != nil {
		return fmt.Errorf("pruning events: %w", err)
	}
	if _, err := tx.Exec(`
INSERT INTO meta (key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value
WHERE CAST(excluded.value AS INTEGER) > CAST(meta.value AS INTEGER)`,
		metaEventsPrunedThrough, fmt.Sprintf("%d", highestDeleted)); err != nil {
		return fmt.Errorf("recording prune watermark: %w", err)
	}
	return tx.Commit()
}
