package whatsapp

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/piwi3910/whatsapp-go/internal/models"
	appstore "github.com/piwi3910/whatsapp-go/internal/store"
)

// newWorkerTestClient builds a Client over fresh temp-dir stores (the same
// shape the CLI creates on first run) so the event worker can be exercised
// end to end without a WhatsApp connection.
func newWorkerTestClient(t *testing.T) *Client {
	t.Helper()
	dir := t.TempDir()
	st, err := appstore.New(filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatalf("appstore.New: %v", err)
	}
	c, err := New(st, filepath.Join(dir, "whatsmeow.db"), waLog.Noop)
	if err != nil {
		st.Close()
		t.Fatalf("whatsapp.New: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// waitFor polls cond until it returns true or the deadline expires.
func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestEventWorkerDeliversInOrder(t *testing.T) {
	c := newWorkerTestClient(t)
	c.SetupEventHandlers()

	var mu sync.Mutex
	var got []models.Event
	c.RegisterEventHandler(func(e models.Event) {
		mu.Lock()
		got = append(got, e)
		mu.Unlock()
	})

	const n = 50
	for i := 0; i < n; i++ {
		c.dispatch(models.Event{Type: models.EventMessageReceived, Payload: []byte("{}"), Timestamp: int64(i)})
	}

	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got) == n
	}, "all events delivered")

	// A single worker must preserve event order.
	mu.Lock()
	for i, e := range got {
		if e.Timestamp != int64(i) {
			t.Fatalf("event %d delivered out of order (timestamp %d)", i, e.Timestamp)
		}
	}
	mu.Unlock()

	// Delivery must be durable: the same events are in the store.
	evts, err := c.store.GetEvents(0, n)
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}
	if len(evts) != n {
		t.Fatalf("stored %d events, want %d", len(evts), n)
	}
}

// TestEventWorkerDropsWhenFull covers the bounded-queue contract (issue #26):
// a saturated store must not stall the whatsmeow event loop, so excess
// events are dropped and counted rather than queued without limit.
func TestEventWorkerDropsWhenFull(t *testing.T) {
	c := newWorkerTestClient(t)
	// Deliberately no SetupEventHandlers: with no worker consuming, the
	// buffer is the whole story.
	for i := 0; i <= eventQueueSize; i++ {
		c.dispatch(models.Event{Type: models.EventMessageReceived, Payload: []byte("{}")})
	}
	if c.eventDropped != 1 {
		t.Fatalf("eventDropped = %d, want 1 (exactly one event over the buffer)", c.eventDropped)
	}
}

// TestEventWorkerDrainsOnClose: events already in the queue when the client
// closes are still stored, so a shutdown cannot silently eat the tail of the
// stream.
func TestEventWorkerDrainsOnClose(t *testing.T) {
	c := newWorkerTestClient(t)
	c.SetupEventHandlers()

	c.dispatch(models.Event{Type: models.EventMessageSent, Payload: []byte("{}"), Timestamp: 1})
	c.dispatch(models.Event{Type: models.EventMessageSent, Payload: []byte("{}"), Timestamp: 2})

	c.Close()

	waitFor(t, func() bool {
		evts, err := c.store.GetEvents(0, 100)
		return err == nil && len(evts) == 2
	}, "queued events to be stored after Close")
}
