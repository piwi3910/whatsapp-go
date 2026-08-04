package webhook

import (
	"sync"
	"time"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

// DeadLetter records one event that exhausted every delivery attempt.
//
// Why a callback plus an in-memory ring, and NOT a SQLite table:
//
// The requirement is "an operator can find out that deliveries are being
// lost". A dead_letters table would satisfy that, but it would also make
// this package depend on internal/store, which today it deliberately does
// not — the dispatcher is constructed by both cmd/wa and internal/api and is
// usable as a library without a database. It would also need its own
// retention/pruning policy, or an endpoint that fails a receiver by filling
// the disk, which is a worse failure than the one being fixed.
//
// So the sink is inverted instead: every exhausted event is (a) logged at
// ERROR with the webhook id, event type and last failure, (b) counted in
// Stats so a /metrics surface can alert on it, and (c) handed to an optional
// OnDeadLetter callback the embedder can point at a database, a file, or a
// queue. The last DeadLetterHistory records are also retained in memory and
// readable via Dispatcher.DeadLetters() for immediate post-mortem, with an
// explicit cap so the retention is bounded.
type DeadLetter struct {
	WebhookID string       `json:"webhook_id"`
	URL       string       `json:"url"`
	Event     models.Event `json:"event"`
	Attempts  int          `json:"attempts"`
	// LastStatus is the final HTTP status, or 0 if the request never got a
	// response (connection refused, timeout, policy refusal, redirect).
	LastStatus int    `json:"last_status,omitempty"`
	LastError  string `json:"last_error,omitempty"`
	At         time.Time
}

// deadLetterRing is a fixed-size, overwrite-oldest buffer of DeadLetter
// records. Bounded by construction: it never allocates after the first
// DeadLetterHistory records.
type deadLetterRing struct {
	mu    sync.Mutex
	buf   []DeadLetter
	next  int
	total uint64
}

func newDeadLetterRing(size int) *deadLetterRing {
	if size < 0 {
		size = 0
	}
	return &deadLetterRing{buf: make([]DeadLetter, 0, size)}
}

func (r *deadLetterRing) add(dl DeadLetter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.total++
	if cap(r.buf) == 0 {
		return
	}
	if len(r.buf) < cap(r.buf) {
		r.buf = append(r.buf, dl)
		r.next = len(r.buf) % cap(r.buf)
		return
	}
	r.buf[r.next] = dl
	r.next = (r.next + 1) % cap(r.buf)
}

// snapshot returns the retained records, oldest first.
func (r *deadLetterRing) snapshot() []DeadLetter {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]DeadLetter, 0, len(r.buf))
	if len(r.buf) < cap(r.buf) {
		out = append(out, r.buf...)
		return out
	}
	out = append(out, r.buf[r.next:]...)
	out = append(out, r.buf[:r.next]...)
	return out
}
