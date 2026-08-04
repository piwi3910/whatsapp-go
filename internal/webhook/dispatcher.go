package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

// Defaults for Options. Chosen for a single-tenant server with a handful of
// webhooks: the queue holds a few seconds of a history-sync burst, and the
// retry ladder gives a restarting receiver ~15s to come back without pinning
// a worker for a minute.
const (
	DefaultQueueSize         = 256
	DefaultMaxAttempts       = 4
	DefaultBaseBackoff       = time.Second
	DefaultMaxBackoff        = 30 * time.Second
	DefaultTimeout           = 10 * time.Second
	DefaultDeadLetterHistory = 64
	maxDrainedResponseBytes  = 32 << 10
	// dropLogInterval throttles the queue-full warning after the first one.
	dropLogInterval = 100
)

// Source says where a webhook registration came from. It is what makes
// config-file webhooks distinguishable from API-managed ones at runtime.
type Source string

const (
	// SourceAPI marks webhooks created through the REST API and persisted in
	// the store. They can be listed, created and deleted through the API.
	SourceAPI Source = "api"
	// SourceConfig marks webhooks declared in the configuration file. They
	// are read-only through the API: the config file is the source of truth,
	// so deleting one through the API could not stick past a restart.
	SourceConfig Source = "config"
)

// ConfigIDPrefix namespaces the IDs of config-file webhooks.
//
// The ':' is deliberate: API-generated IDs are "wh_" + 32 hex characters, so
// no API-created webhook can ever collide with a config one, which is what
// stops a stored webhook and a config webhook from silently overwriting each
// other depending on which happened to be registered last at boot. Anything
// that does land in this namespace is classified as config-managed and is
// read-only through the API, and a replacement across sources is logged
// rather than applied silently.
const ConfigIDPrefix = "cfg:"

// legacyConfigIDPrefix is the pre-namespace form ("cfg_0", "cfg_1", ...).
// Still recognised as config-managed so an older caller that has not moved
// to ConfigWebhookID gets the correct read-only behaviour rather than a
// confusing 404 from the store.
const legacyConfigIDPrefix = "cfg_"

// ConfigWebhookID returns the namespaced ID for the i-th webhook declared in
// the configuration file.
func ConfigWebhookID(i int) string { return fmt.Sprintf("%s%d", ConfigIDPrefix, i) }

// IsConfigManaged reports whether id belongs to the config-file namespace.
func IsConfigManaged(id string) bool {
	return strings.HasPrefix(id, ConfigIDPrefix) || strings.HasPrefix(id, legacyConfigIDPrefix)
}

// ErrClosed is returned by Register after Shutdown.
var ErrClosed = errors.New("webhook dispatcher is shut down")

// Options configures a Dispatcher. The zero value is usable: every field
// falls back to the Default* constant above.
type Options struct {
	// Policy governs which targets are reachable (see safeurl.go).
	Policy Policy
	// QueueSize caps the number of events buffered per webhook. When the cap
	// is reached the oldest queued event is dropped (see queue.push).
	QueueSize int
	// MaxAttempts is the total number of delivery attempts per event,
	// including the first.
	MaxAttempts int
	// BaseBackoff/MaxBackoff bound the jittered exponential retry ladder.
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
	// Timeout bounds a single HTTP attempt.
	Timeout time.Duration
	// DeadLetterHistory is how many exhausted deliveries to retain in memory
	// for DeadLetters(). Zero uses the default; a negative value disables
	// retention (the callback and the counters still fire).
	DeadLetterHistory int
	// OnDeadLetter, if set, is called once per event that exhausts every
	// attempt. It runs on the delivering worker, so a slow callback stalls
	// that webhook's queue — hand off to your own buffer if it can block.
	OnDeadLetter func(DeadLetter)
	// Logger receives delivery diagnostics. Defaults to slog.Default().
	Logger *slog.Logger
}

func (o *Options) applyDefaults() {
	if o.QueueSize <= 0 {
		o.QueueSize = DefaultQueueSize
	}
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = DefaultMaxAttempts
	}
	if o.BaseBackoff <= 0 {
		o.BaseBackoff = DefaultBaseBackoff
	}
	if o.MaxBackoff <= 0 {
		o.MaxBackoff = DefaultMaxBackoff
	}
	if o.MaxBackoff < o.BaseBackoff {
		o.MaxBackoff = o.BaseBackoff
	}
	if o.Timeout <= 0 {
		o.Timeout = DefaultTimeout
	}
	if o.DeadLetterHistory == 0 {
		o.DeadLetterHistory = DefaultDeadLetterHistory
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
}

// registration is one webhook plus the machinery that delivers to it.
type registration struct {
	wh     models.Webhook
	source Source
	q      *queue
	// stop is closed when this specific registration goes away (Unregister,
	// or replacement by a re-Register of the same ID). Closing it abandons
	// whatever is still queued, which is the right call: the operator
	// deleted the webhook.
	stop chan struct{}
	// done is closed by the worker as it exits. Tests and Unregister use it
	// to observe worker lifetime without polling.
	done chan struct{}
}

// Dispatcher manages webhook delivery: bounded per-webhook queues, one
// worker each (so deliveries to a given endpoint stay in order), jittered
// exponential retries, and a dead-letter sink for events that never land.
//
// A Dispatcher must be shut down with Shutdown to stop its workers; the
// process should do that before closing anything the callbacks touch.
type Dispatcher struct {
	mu       sync.RWMutex
	webhooks map[string]*registration
	closed   bool

	opts   Options
	client *http.Client

	// ctx is cancelled to abort in-flight requests and pending backoff
	// waits. draining is closed first, by Shutdown, to ask workers to finish
	// what is already queued; ctx is only cancelled once the drain deadline
	// passes (or the drain completes).
	ctx      context.Context
	cancel   context.CancelFunc
	draining chan struct{}
	wg       sync.WaitGroup

	dead *deadLetterRing

	enqueued  atomic.Uint64
	delivered atomic.Uint64
	failed    atomic.Uint64
	retried   atomic.Uint64
}

// New creates a Dispatcher that refuses loopback and private targets — the
// safe default for a server that may be internet-reachable. Use
// NewWithPolicy or NewWithOptions when the receiver is deliberately on a
// private network.
func New() *Dispatcher {
	return NewWithPolicy(Policy{})
}

// NewWithPolicy creates a Dispatcher whose outbound requests are governed by
// p, both at registration (Policy.ValidateURL) and at dial time.
func NewWithPolicy(p Policy) *Dispatcher {
	return NewWithOptions(Options{Policy: p})
}

// NewWithOptions creates a Dispatcher with full control over queueing and
// retry behaviour.
func NewWithOptions(opts Options) *Dispatcher {
	opts.applyDefaults()
	ctx, cancel := context.WithCancel(context.Background())
	return &Dispatcher{
		webhooks: make(map[string]*registration),
		opts:     opts,
		client:   newSafeClient(opts.Timeout, opts.Policy),
		ctx:      ctx,
		cancel:   cancel,
		draining: make(chan struct{}),
		dead:     newDeadLetterRing(opts.DeadLetterHistory),
	}
}

// Policy returns the dispatcher's target policy, so callers validating a URL
// before registering it apply exactly the rules the dispatcher enforces.
func (d *Dispatcher) Policy() Policy { return d.opts.Policy }

// Register adds (or replaces) an API/store-sourced webhook.
//
// An ID that already sits in the config namespace is classified as
// config-managed rather than rejected. That is a compatibility shim for
// callers still constructing "cfg_N" IDs by hand instead of calling
// RegisterConfig, and it is what keeps such a webhook read-only through the
// API. New code should call RegisterConfig.
func (d *Dispatcher) Register(wh models.Webhook) error {
	if IsConfigManaged(wh.ID) {
		d.opts.Logger.Warn("webhook registered with a config-namespace id; use RegisterConfig",
			"webhook_id", wh.ID)
		return d.register(wh, SourceConfig)
	}
	return d.register(wh, SourceAPI)
}

// RegisterConfig adds the i-th webhook declared in the configuration file,
// rewriting its ID into the config namespace, and returns the registration
// as stored. Config webhooks are read-only through the REST API.
func (d *Dispatcher) RegisterConfig(i int, wh models.Webhook) (models.Webhook, error) {
	wh.ID = ConfigWebhookID(i)
	if err := d.register(wh, SourceConfig); err != nil {
		return wh, err
	}
	return wh, nil
}

func (d *Dispatcher) register(wh models.Webhook, src Source) error {
	reg := &registration{
		wh:     wh,
		source: src,
		q:      newQueue(d.opts.QueueSize),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}

	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return ErrClosed
	}
	if old, ok := d.webhooks[wh.ID]; ok {
		// Replacing a registration: retire the old worker so we never end up
		// with two workers racing on one endpoint (which would reorder).
		close(old.stop)
		if old.source != src || old.wh.URL != wh.URL {
			// A collision across sources is a configuration mistake worth
			// shouting about — it used to happen silently at boot, where
			// registration order decided which endpoint actually received
			// events.
			d.opts.Logger.Warn("webhook registration replaced an existing one with the same id",
				"webhook_id", wh.ID, "old_source", old.source, "new_source", src)
		}
	}
	d.webhooks[wh.ID] = reg
	d.mu.Unlock()

	d.wg.Add(1)
	go d.worker(reg)
	return nil
}

// Unregister removes a webhook and abandons anything still queued for it.
func (d *Dispatcher) Unregister(id string) {
	d.mu.Lock()
	reg, ok := d.webhooks[id]
	if ok {
		delete(d.webhooks, id)
	}
	d.mu.Unlock()
	if ok {
		close(reg.stop)
	}
}

// Registration is a webhook together with where it came from.
type Registration struct {
	models.Webhook
	Source Source `json:"source"`
}

// List returns every registered webhook, sorted by ID for a stable
// response. Used by the API to surface config-managed webhooks alongside
// stored ones.
func (d *Dispatcher) List() []Registration {
	d.mu.RLock()
	out := make([]Registration, 0, len(d.webhooks))
	for _, reg := range d.webhooks {
		out = append(out, Registration{Webhook: reg.wh, Source: reg.source})
	}
	d.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Dispatch enqueues an event for every matching webhook. It never blocks and
// never spawns a goroutine: the per-webhook worker does the work. After
// Shutdown it is a no-op, so a late event from the WhatsApp client during
// teardown cannot resurrect delivery goroutines.
func (d *Dispatcher) Dispatch(evt models.Event) {
	d.mu.RLock()
	if d.closed {
		d.mu.RUnlock()
		return
	}
	targets := make([]*registration, 0, len(d.webhooks))
	for _, reg := range d.webhooks {
		if matchesEvent(reg.wh.Events, evt.Type) {
			targets = append(targets, reg)
		}
	}
	d.mu.RUnlock()

	for _, reg := range targets {
		total, dropped := reg.q.push(evt)
		d.enqueued.Add(1)
		if dropped {
			// Log the first drop and then every hundredth: a wedged receiver
			// would otherwise emit one line per event and bury the incident
			// it is trying to report.
			if total == 1 || total%dropLogInterval == 0 {
				d.opts.Logger.Warn("webhook queue full; dropped oldest queued event",
					"webhook_id", reg.wh.ID, "queue_size", d.opts.QueueSize,
					"dropped_total", total, "event", evt.Type)
			}
		}
	}
}

func matchesEvent(subscribed []string, eventType string) bool {
	for _, s := range subscribed {
		if s == "*" || s == eventType {
			return true
		}
	}
	return false
}

// worker drains one webhook's queue, one event at a time and in order.
func (d *Dispatcher) worker(reg *registration) {
	defer d.wg.Done()
	defer close(reg.done)

	for {
		// Check stop BEFORE popping. Popping first means a webhook that was
		// just deleted keeps delivering its whole backlog to the removed
		// endpoint — for a failing endpoint, potentially hours of retries
		// against a URL the operator explicitly revoked — and lets a
		// re-registered id run two workers against the same webhook at once.
		select {
		case <-reg.stop:
			if n := reg.q.depth(); n > 0 {
				d.opts.Logger.Info("discarding queued events for unregistered webhook",
					"webhook_id", reg.wh.ID, "queued", n)
			}
			return
		default:
		}

		evt, ok := reg.q.pop()
		if ok {
			d.deliver(reg, evt)
			continue
		}
		select {
		case <-reg.q.notify:
			// Something was pushed (or a coalesced wakeup); loop and pop.
		case <-reg.stop:
			// The webhook was deleted or replaced. Anything still queued is
			// intentionally discarded — the operator asked for that — so it
			// is reported as a count, not as dead letters.
			if n := reg.q.depth(); n > 0 {
				d.opts.Logger.Info("discarding queued events for unregistered webhook",
					"webhook_id", reg.wh.ID, "queued", n)
			}
			return
		case <-d.ctx.Done():
			d.abandonQueue(reg, "dispatcher cancelled before delivery")
			return
		case <-d.draining:
			// Shutdown asked for a final flush: deliver whatever is already
			// queued, then exit. Dispatch is closed by now, so the queue can
			// only shrink and this terminates.
			for d.ctx.Err() == nil {
				evt, ok := reg.q.pop()
				if !ok {
					return
				}
				d.deliver(reg, evt)
			}
			d.abandonQueue(reg, "shutdown drain deadline exceeded")
			return
		}
	}
}

// abandonQueue dead-letters everything still queued when a worker is torn
// down without draining. These events were accepted and then never
// delivered, so they must show up somewhere an operator can see; the
// per-event ERROR line is suppressed in favour of one summary line, because
// a full queue would otherwise emit hundreds of them during shutdown.
func (d *Dispatcher) abandonQueue(reg *registration, reason string) {
	n := 0
	for {
		evt, ok := reg.q.pop()
		if !ok {
			break
		}
		d.deadLetter(reg, evt, 0, 0, reason, false)
		n++
	}
	if n > 0 {
		d.opts.Logger.Error("webhook events abandoned undelivered",
			"webhook_id", reg.wh.ID, "count", n, "reason", reason)
	}
}

// webhookPayload is the JSON body sent to webhook URLs.
type webhookPayload struct {
	Event     string          `json:"event"`
	Timestamp int64           `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

// deliver POSTs one event, retrying with jittered exponential backoff until
// it succeeds, the attempts run out, or the dispatcher is torn down.
func (d *Dispatcher) deliver(reg *registration, evt models.Event) {
	wh := reg.wh
	payload := webhookPayload{
		Event:     evt.Type,
		Timestamp: evt.Timestamp,
		Data:      evt.Payload,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		// Unencodable payload: retrying cannot help, but losing it silently
		// is what this whole change is about, so dead-letter it.
		d.recordDeadLetter(reg, evt, 0, 0, fmt.Sprintf("marshal payload: %v", err))
		return
	}

	// HMAC is computed once over the exact bytes on the wire and is
	// unchanged from the original implementation: same body shape, same
	// "sha256=" + hex-of-HMAC-SHA256 value, same X-Wa-Signature header. A
	// timestamp is deliberately NOT folded into the signed string — the body
	// already carries the event timestamp, and changing the signing input
	// would silently break every existing receiver's verification. Replay
	// protection, if it is ever wanted, belongs in a new header with its own
	// signature so old receivers keep working.
	var sig string
	if wh.Secret != "" {
		mac := hmac.New(sha256.New, []byte(wh.Secret))
		mac.Write(body)
		sig = "sha256=" + hex.EncodeToString(mac.Sum(nil))
	}

	backoff := d.opts.BaseBackoff
	var lastErr string
	var lastStatus int

	for attempt := 1; attempt <= d.opts.MaxAttempts; attempt++ {
		if attempt > 1 {
			d.retried.Add(1)
			if !d.sleep(jitterBackoff(backoff)) {
				// Torn down mid-ladder. Record it rather than vanishing:
				// from the receiver's point of view the event was lost.
				d.recordDeadLetter(reg, evt, attempt-1, lastStatus,
					"dispatcher shut down before retry: "+lastErr)
				return
			}
			backoff = min(backoff*2, d.opts.MaxBackoff)
		}

		status, err := d.attempt(wh.URL, body, sig)
		if err != nil {
			lastErr = err.Error()
			lastStatus = 0
			d.opts.Logger.Warn("webhook delivery attempt failed",
				"webhook_id", wh.ID, "attempt", attempt, "event", evt.Type, "error", err)
			continue
		}
		lastStatus = status
		if status >= 200 && status < 300 {
			d.delivered.Add(1)
			return
		}
		lastErr = fmt.Sprintf("http %d", status)
		d.opts.Logger.Warn("webhook delivery attempt rejected",
			"webhook_id", wh.ID, "attempt", attempt, "event", evt.Type, "status", status)
	}

	d.recordDeadLetter(reg, evt, d.opts.MaxAttempts, lastStatus, lastErr)
}

// attempt performs a single POST and returns the status code. The response
// body is always drained (bounded) and closed, so the connection can go back
// to the idle pool instead of being torn down after every delivery.
func (d *Dispatcher) attempt(url string, body []byte, sig string) (int, error) {
	req, err := http.NewRequestWithContext(d.ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if sig != "" {
		req.Header.Set("X-Wa-Signature", sig)
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	// Bounded drain: a cooperative receiver replies with little or nothing,
	// and a hostile one must not be able to make us read forever.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrainedResponseBytes))
	return resp.StatusCode, nil
}

// sleep waits for d, returning false if the dispatcher was cancelled first.
func (d *Dispatcher) sleep(dur time.Duration) bool {
	if dur <= 0 {
		return d.ctx.Err() == nil
	}
	t := time.NewTimer(dur)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-d.ctx.Done():
		return false
	}
}

// jitterBackoff spreads a retry over [d/2, d). Without jitter, a receiver
// that just restarted gets every queued webhook's retry in the same
// millisecond from every replica — the thundering herd that keeps it down.
func jitterBackoff(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	half := d / 2
	return half + time.Duration(rand.Int64N(int64(half)+1))
}

func (d *Dispatcher) recordDeadLetter(reg *registration, evt models.Event, attempts, status int, lastErr string) {
	d.deadLetter(reg, evt, attempts, status, lastErr, true)
}

func (d *Dispatcher) deadLetter(reg *registration, evt models.Event, attempts, status int, lastErr string, logEach bool) {
	d.failed.Add(1)
	dl := DeadLetter{
		WebhookID:  reg.wh.ID,
		URL:        reg.wh.URL,
		Event:      evt,
		Attempts:   attempts,
		LastStatus: status,
		LastError:  lastErr,
		At:         time.Now(),
	}
	d.dead.add(dl)
	if logEach {
		d.opts.Logger.Error("webhook event dead-lettered after exhausting delivery attempts",
			"webhook_id", dl.WebhookID, "event", evt.Type, "attempts", attempts,
			"last_status", status, "last_error", lastErr)
	}
	if d.opts.OnDeadLetter != nil {
		d.opts.OnDeadLetter(dl)
	}
}

// DeadLetters returns the retained dead-letter records, oldest first.
func (d *Dispatcher) DeadLetters() []DeadLetter { return d.dead.snapshot() }

// Stats is a point-in-time snapshot of dispatcher counters, suitable for a
// /metrics surface.
type Stats struct {
	Enqueued     uint64 `json:"enqueued"`
	Delivered    uint64 `json:"delivered"`
	Retries      uint64 `json:"retries"`
	DeadLettered uint64 `json:"dead_lettered"`
	Dropped      uint64 `json:"dropped"`
	QueueDepth   int    `json:"queue_depth"`
	Webhooks     int    `json:"webhooks"`
}

// Stats reports delivery counters. Dropped and QueueDepth are summed across
// all registered webhooks.
func (d *Dispatcher) Stats() Stats {
	s := Stats{
		Enqueued:     d.enqueued.Load(),
		Delivered:    d.delivered.Load(),
		Retries:      d.retried.Load(),
		DeadLettered: d.failed.Load(),
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	s.Webhooks = len(d.webhooks)
	for _, reg := range d.webhooks {
		s.QueueDepth += reg.q.depth()
		s.Dropped += reg.q.droppedCount()
	}
	return s
}

// Shutdown stops accepting events, gives the workers until ctx expires to
// flush what is already queued, and returns once every worker has exited.
//
// It is safe to call more than once and always leaves the dispatcher with no
// running goroutines, so the caller may then close the store, the database
// and anything an OnDeadLetter callback touches. Returns ctx.Err() if the
// deadline hit before the queues drained (the remaining events are
// dead-lettered by the workers as they unwind).
func (d *Dispatcher) Shutdown(ctx context.Context) error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		// Still wait: a concurrent Shutdown may not have finished, and
		// returning early would defeat the point of the call.
		d.wg.Wait()
		return nil
	}
	d.closed = true
	close(d.draining)
	d.mu.Unlock()

	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		d.cancel()
		d.sweepAbandoned("queued during shutdown")
		d.client.CloseIdleConnections()
		return nil
	case <-ctx.Done():
		// Deadline hit: cancel in-flight requests and pending backoff waits
		// so the workers unwind now instead of holding the process open for
		// the rest of the retry ladder.
		d.cancel()
		<-done
		d.sweepAbandoned("queued during shutdown")
		d.client.CloseIdleConnections()
		return ctx.Err()
	}
}

// sweepAbandoned dead-letters anything still sitting in a queue after every
// worker has exited.
//
// Dispatch releases the lock before pushing, so an event can land in a queue
// whose worker has already drained to empty and returned. Without this sweep
// that event would be counted as enqueued and then vanish — never delivered,
// never dead-lettered, never logged — which is exactly the invariant the
// dispatcher promises not to break. Must be called only after wg.Wait().
func (d *Dispatcher) sweepAbandoned(reason string) {
	d.mu.RLock()
	regs := make([]*registration, 0, len(d.webhooks))
	for _, reg := range d.webhooks {
		regs = append(regs, reg)
	}
	d.mu.RUnlock()

	for _, reg := range regs {
		d.abandonQueue(reg, reason)
	}
}

// Close shuts the dispatcher down with the given context. It is an alias for
// Shutdown, for callers that expect the io.Closer-ish spelling.
func (d *Dispatcher) Close(ctx context.Context) error { return d.Shutdown(ctx) }
