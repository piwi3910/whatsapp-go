package webhook

import (
	"sync"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

// queue is a bounded FIFO of events pending delivery to ONE webhook.
//
// Why one queue per webhook rather than a shared pool:
//
//   - Ordering. A receiver that is told "message.received" then
//     "message.deleted" must see them in that order; a shared worker pool
//     delivers concurrently and reorders them. One queue drained by one
//     worker gives per-endpoint ordering for free.
//   - Isolation. A single slow or dead endpoint cannot stall deliveries to
//     the others, because it only ever occupies its own worker.
//
// The cost is head-of-line blocking within a webhook: a retrying event
// delays the ones behind it. That is deliberate — it is the price of
// ordering — and the depth cap below is what keeps it from turning into
// unbounded memory growth.
type queue struct {
	mu    sync.Mutex
	items []models.Event
	cap   int
	// dropped counts events discarded because the queue was full, for the
	// life of the queue. Reported in Stats and in the drop log line.
	dropped uint64
	// notify carries a single edge-triggered wakeup for the worker. Cap 1
	// and a non-blocking send: pushes never block on a worker that is busy
	// delivering, and a coalesced wakeup is harmless because the worker
	// always drains to empty before waiting again.
	notify chan struct{}
}

func newQueue(capacity int) *queue {
	if capacity < 1 {
		capacity = 1
	}
	return &queue{
		items:  make([]models.Event, 0, capacity),
		cap:    capacity,
		notify: make(chan struct{}, 1),
	}
}

// push appends evt, evicting the OLDEST queued event when the queue is full.
// It reports how many events have been dropped in total, and whether this
// push caused a drop.
//
// Drop-oldest, not drop-newest, because webhook consumers care most about
// current state: when a receiver has been down long enough to fill the
// queue, the freshest events are the ones that still describe reality, and
// the oldest are the ones most likely to be stale by the time the receiver
// recovers. Drop-oldest also keeps the queue advancing instead of wedging on
// a backlog nobody will ever want. The trade-off — a gap in the middle of an
// ordered stream — is made visible by the logged drop counter rather than
// hidden.
func (q *queue) push(evt models.Event) (total uint64, dropped bool) {
	q.mu.Lock()
	if len(q.items) >= q.cap {
		// Evict the head. Copying keeps the backing array from growing.
		copy(q.items, q.items[1:])
		q.items = q.items[:len(q.items)-1]
		q.dropped++
		dropped = true
	}
	q.items = append(q.items, evt)
	total = q.dropped
	q.mu.Unlock()

	select {
	case q.notify <- struct{}{}:
	default:
	}
	return total, dropped
}

// pop removes and returns the head of the queue. ok is false when empty.
func (q *queue) pop() (models.Event, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return models.Event{}, false
	}
	evt := q.items[0]
	// Shift in place rather than resliceing from the front: that keeps the
	// backing array stable (no reallocation churn) and clears the vacated
	// slot so a large payload is not pinned by it. O(depth) per pop is
	// irrelevant next to the HTTP request that follows.
	copy(q.items, q.items[1:])
	q.items[len(q.items)-1] = models.Event{}
	q.items = q.items[:len(q.items)-1]
	return evt, true
}

// depth returns the number of queued events.
func (q *queue) depth() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

// droppedCount returns the total number of events evicted by the depth cap.
func (q *queue) droppedCount() uint64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.dropped
}
