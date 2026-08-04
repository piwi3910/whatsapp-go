package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

// newTestDispatcher builds a dispatcher that can reach the loopback test
// servers, keeps its diagnostics out of the test log, and is always shut
// down — so a leaked worker shows up as a hang or a failed leak check rather
// than as background noise in an unrelated test.
func newTestDispatcher(t *testing.T, opts Options) *Dispatcher {
	t.Helper()
	opts.Policy.AllowPrivateTargets = true
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	d := NewWithOptions(opts)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = d.Shutdown(ctx)
	})
	return d
}

func evt(i int) models.Event {
	return models.Event{
		Type:      "message.received",
		Payload:   json.RawMessage(fmt.Sprintf(`{"n":%d}`, i)),
		Timestamp: time.Now().Unix(),
	}
}

// recorder collects the request bodies a test server received, in order.
type recorder struct {
	mu   sync.Mutex
	body []string
	got  chan struct{}
}

func newRecorder() *recorder {
	return &recorder{got: make(chan struct{}, 1024)}
}

func (r *recorder) record(b []byte) {
	r.mu.Lock()
	r.body = append(r.body, string(b))
	r.mu.Unlock()
	select {
	case r.got <- struct{}{}:
	default:
	}
}

func (r *recorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.body...)
}

// waitFor blocks until n requests have been recorded, or fails.
func (r *recorder) waitFor(t *testing.T, n int, d time.Duration) {
	t.Helper()
	deadline := time.After(d)
	for {
		r.mu.Lock()
		have := len(r.body)
		r.mu.Unlock()
		if have >= n {
			return
		}
		select {
		case <-r.got:
		case <-deadline:
			t.Fatalf("timed out waiting for %d deliveries, got %d", n, have)
		}
	}
}

// waitForStats polls the dispatcher's counters until cond holds.
func waitForStats(t *testing.T, d *Dispatcher, cond func(Stats) bool) Stats {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		s := d.Stats()
		if cond(s) {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting on dispatcher stats: %+v", s)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestDispatch_PreservesOrderPerWebhook is the core ordering guarantee: one
// endpoint sees events in the order they were dispatched. The old
// goroutine-per-event dispatcher failed this routinely.
func TestDispatch_PreservesOrderPerWebhook(t *testing.T) {
	rec := newRecorder()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rec.record(b)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	d := newTestDispatcher(t, Options{QueueSize: 128})
	if err := d.Register(models.Webhook{ID: "wh1", URL: srv.URL, Events: []string{"*"}}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	const n = 40
	for i := 0; i < n; i++ {
		d.Dispatch(evt(i))
	}
	rec.waitFor(t, n, 10*time.Second)

	for i, body := range rec.snapshot() {
		want := fmt.Sprintf(`"data":{"n":%d}`, i)
		if !strings.Contains(body, want) {
			t.Fatalf("delivery %d out of order: body %s, want to contain %s", i, body, want)
		}
	}
}

// TestDispatch_SlowWebhookDoesNotBlockOthers checks the isolation half of
// the per-webhook queue design: a wedged endpoint must not stop deliveries
// to a healthy one.
func TestDispatch_SlowWebhookDoesNotBlockOthers(t *testing.T) {
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(200)
	}))
	defer slow.Close()
	defer close(release)

	rec := newRecorder()
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rec.record(b)
		w.WriteHeader(200)
	}))
	defer fast.Close()

	d := newTestDispatcher(t, Options{})
	_ = d.Register(models.Webhook{ID: "slow", URL: slow.URL, Events: []string{"*"}})
	_ = d.Register(models.Webhook{ID: "fast", URL: fast.URL, Events: []string{"*"}})

	for i := 0; i < 5; i++ {
		d.Dispatch(evt(i))
	}
	// The fast endpoint gets everything even though the slow one is stuck on
	// its very first request.
	rec.waitFor(t, 5, 10*time.Second)
}

// TestQueueFull_DropsOldest locks in the documented overflow policy.
func TestQueueFull_DropsOldest(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	rec := newRecorder()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		select {
		case entered <- struct{}{}:
			// First request: hold the worker so the queue can fill behind it.
			select {
			case <-release:
			case <-r.Context().Done():
			}
		default:
		}
		rec.record(b)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	// Released once, whichever comes first: the happy path below or the
	// cleanup after a t.Fatal (an unreleased handler would hang srv.Close).
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseAll()

	d := newTestDispatcher(t, Options{QueueSize: 2})
	_ = d.Register(models.Webhook{ID: "wh1", URL: srv.URL, Events: []string{"*"}})

	d.Dispatch(evt(0))
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first delivery never started")
	}

	// The worker is parked in the handler, so these queue up. With a cap of
	// 2, events 1, 2 and 3 are evicted and only 4 and 5 survive.
	for i := 1; i <= 5; i++ {
		d.Dispatch(evt(i))
	}
	if got := d.Stats().Dropped; got != 3 {
		t.Fatalf("dropped = %d, want 3", got)
	}

	releaseAll()
	rec.waitFor(t, 3, 10*time.Second)

	bodies := rec.snapshot()
	if len(bodies) != 3 {
		t.Fatalf("got %d deliveries, want exactly 3: %v", len(bodies), bodies)
	}
	// Order is still preserved among the survivors, and it is the NEWEST
	// events that survived.
	for i, want := range []int{0, 4, 5} {
		if !strings.Contains(bodies[i], fmt.Sprintf(`"data":{"n":%d}`, want)) {
			t.Errorf("delivery %d = %s, want event %d", i, bodies[i], want)
		}
	}
}

// TestRetry_BackoffThenSuccess checks that a transient failure is retried,
// that the retries are spaced by the backoff ladder, and that a later
// success ends the ladder.
func TestRetry_BackoffThenSuccess(t *testing.T) {
	var mu sync.Mutex
	attempts := 0
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		mu.Lock()
		attempts++
		n := attempts
		mu.Unlock()
		if n < 3 {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(200)
		select {
		case done <- struct{}{}:
		default:
		}
	}))
	defer srv.Close()

	d := newTestDispatcher(t, Options{MaxAttempts: 4, BaseBackoff: 40 * time.Millisecond})
	_ = d.Register(models.Webhook{ID: "wh1", URL: srv.URL, Events: []string{"*"}})

	start := time.Now()
	d.Dispatch(evt(0))
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("delivery never succeeded")
	}
	elapsed := time.Since(start)

	// Jitter puts each wait in [d/2, d): 20ms minimum before attempt 2 and
	// 40ms minimum before attempt 3. Anything faster means the backoff was
	// skipped entirely.
	if elapsed < 60*time.Millisecond {
		t.Errorf("elapsed = %v, want at least 60ms of jittered backoff", elapsed)
	}
	mu.Lock()
	got := attempts
	mu.Unlock()
	if got != 3 {
		t.Errorf("attempts = %d, want 3", got)
	}

	// The handler signals from inside the request, so the worker has not
	// necessarily recorded the success yet; poll rather than sleep.
	st := waitForStats(t, d, func(s Stats) bool { return s.Delivered == 1 })
	if st.Retries != 2 {
		t.Errorf("retries = %d, want 2", st.Retries)
	}
	if st.DeadLettered != 0 {
		t.Errorf("dead-lettered = %d, want 0", st.DeadLettered)
	}
}

// TestDeadLetter_OnExhaustion is the "operators can find out" guarantee: an
// event that never lands must reach the callback, the in-memory history and
// the counters.
func TestDeadLetter_OnExhaustion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(503)
	}))
	defer srv.Close()

	dlCh := make(chan DeadLetter, 4)
	d := newTestDispatcher(t, Options{
		MaxAttempts:  3,
		BaseBackoff:  time.Millisecond,
		OnDeadLetter: func(dl DeadLetter) { dlCh <- dl },
	})
	_ = d.Register(models.Webhook{ID: "wh1", URL: srv.URL, Events: []string{"*"}})

	d.Dispatch(evt(7))

	var dl DeadLetter
	select {
	case dl = <-dlCh:
	case <-time.After(10 * time.Second):
		t.Fatal("dead-letter callback never fired")
	}

	if dl.WebhookID != "wh1" {
		t.Errorf("webhook id = %q, want wh1", dl.WebhookID)
	}
	if dl.Attempts != 3 {
		t.Errorf("attempts = %d, want 3", dl.Attempts)
	}
	if dl.LastStatus != 503 {
		t.Errorf("last status = %d, want 503", dl.LastStatus)
	}
	if string(dl.Event.Payload) != `{"n":7}` {
		t.Errorf("event payload = %q, want the dispatched event", dl.Event.Payload)
	}

	history := d.DeadLetters()
	if len(history) != 1 || history[0].WebhookID != "wh1" {
		t.Fatalf("DeadLetters() = %+v, want one record for wh1", history)
	}
	if got := d.Stats().DeadLettered; got != 1 {
		t.Errorf("dead-lettered counter = %d, want 1", got)
	}
}

// TestShutdown_DrainsInFlight: a shutdown with room to spare must not lose
// events that were already accepted.
func TestShutdown_DrainsInFlight(t *testing.T) {
	rec := newRecorder()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		time.Sleep(50 * time.Millisecond) // slow enough that a queue forms
		rec.record(b)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	d := NewWithOptions(Options{
		Policy: Policy{AllowPrivateTargets: true},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	_ = d.Register(models.Webhook{ID: "wh1", URL: srv.URL, Events: []string{"*"}})

	for i := 0; i < 4; i++ {
		d.Dispatch(evt(i))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := d.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	// Shutdown returned, so every worker has exited; the deliveries must
	// therefore already be recorded — no sleep required.
	if got := len(rec.snapshot()); got != 4 {
		t.Fatalf("delivered %d of 4 queued events before shutdown completed", got)
	}
	if got := d.Stats().Delivered; got != 4 {
		t.Errorf("delivered counter = %d, want 4", got)
	}
}

// TestShutdown_DeadlineAbortsAndDeadLetters: when the drain deadline passes,
// shutdown must return promptly rather than holding the process open for the
// rest of the retry ladder, and the abandoned events must be recorded.
func TestShutdown_DeadlineAbortsAndDeadLetters(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	defer close(release)

	d := NewWithOptions(Options{
		Policy: Policy{AllowPrivateTargets: true},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	_ = d.Register(models.Webhook{ID: "wh1", URL: srv.URL, Events: []string{"*"}})
	for i := 0; i < 3; i++ {
		d.Dispatch(evt(i))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := d.Shutdown(ctx)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Shutdown returned nil, want the deadline error")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("Shutdown took %v; it must abort in-flight work at the deadline", elapsed)
	}
	// Nothing was delivered, so all three events must be accounted for as
	// dead letters rather than silently vanishing.
	if got := d.Stats().DeadLettered; got != 3 {
		t.Errorf("dead-lettered = %d, want 3", got)
	}
}

// TestShutdown_NoGoroutineLeak asserts the whole point of the rewrite: no
// delivery goroutine outlives the dispatcher.
func TestShutdown_NoGoroutineLeak(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	// Warm the server so its own accept/conn goroutines are already counted.
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("warmup: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	runtime.GC()
	before := runtime.NumGoroutine()

	d := NewWithOptions(Options{
		Policy: Policy{AllowPrivateTargets: true},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	var regs []*registration
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("wh%d", i)
		_ = d.Register(models.Webhook{ID: id, URL: srv.URL, Events: []string{"*"}})
		d.mu.RLock()
		regs = append(regs, d.webhooks[id])
		d.mu.RUnlock()
	}
	for i := 0; i < 200; i++ {
		d.Dispatch(evt(i))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := d.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	// Strongest signal first: every worker signalled its own exit. This does
	// not depend on scheduler timing the way a goroutine count does.
	for i, reg := range regs {
		select {
		case <-reg.done:
		default:
			t.Fatalf("worker %d still running after Shutdown", i)
		}
	}

	// Then the coarse check, polled because HTTP connection goroutines unwind
	// asynchronously after CloseIdleConnections.
	deadline := time.Now().Add(10 * time.Second)
	for {
		runtime.GC()
		after := runtime.NumGoroutine()
		if after <= before+2 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutines: before=%d after=%d — delivery goroutines leaked", before, after)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestDispatch_AfterShutdownIsNoop: a late event from the WhatsApp client
// during teardown must not restart delivery, which would race the caller's
// deferred store.Close.
func TestDispatch_AfterShutdownIsNoop(t *testing.T) {
	var mu sync.Mutex
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		called = true
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer srv.Close()

	d := newTestDispatcher(t, Options{})
	_ = d.Register(models.Webhook{ID: "wh1", URL: srv.URL, Events: []string{"*"}})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	d.Dispatch(evt(0))
	if err := d.Register(models.Webhook{ID: "wh2", URL: srv.URL, Events: []string{"*"}}); err != ErrClosed {
		t.Errorf("Register after Shutdown = %v, want ErrClosed", err)
	}

	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if called {
		t.Error("event was delivered after Shutdown")
	}
	if d.Stats().QueueDepth != 0 {
		t.Error("event was queued after Shutdown")
	}
}

// TestUnregister_StopsWorker checks that deleting a webhook actually retires
// its goroutine instead of leaving it parked forever.
func TestUnregister_StopsWorker(t *testing.T) {
	d := newTestDispatcher(t, Options{})
	_ = d.Register(models.Webhook{ID: "wh1", URL: "http://192.0.2.1/hook", Events: []string{"*"}})

	d.mu.RLock()
	reg := d.webhooks["wh1"]
	d.mu.RUnlock()

	d.Unregister("wh1")
	select {
	case <-reg.done:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not exit after Unregister")
	}
	if len(d.List()) != 0 {
		t.Error("webhook still listed after Unregister")
	}
}

// TestHMACSignature_ExactValue pins the signing contract. The signature is
// HMAC-SHA256 over the exact request body, hex encoded and prefixed with
// "sha256=" — changing any of that silently breaks every receiver, so it is
// asserted by value and not just by shape.
func TestHMACSignature_ExactValue(t *testing.T) {
	type capture struct {
		body []byte
		sig  string
	}
	ch := make(chan capture, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		select {
		case ch <- capture{body: b, sig: r.Header.Get("X-Wa-Signature")}:
		default:
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	d := newTestDispatcher(t, Options{})
	_ = d.Register(models.Webhook{ID: "wh1", URL: srv.URL, Events: []string{"*"}, Secret: "mysecret"})
	d.Dispatch(models.Event{Type: "test", Payload: json.RawMessage(`{"a":1}`), Timestamp: 1700000000})

	var got capture
	select {
	case got = <-ch:
	case <-time.After(10 * time.Second):
		t.Fatal("webhook was not delivered")
	}

	if want := `{"event":"test","timestamp":1700000000,"data":{"a":1}}`; string(got.body) != want {
		t.Fatalf("body = %s, want %s", got.body, want)
	}
	mac := hmac.New(sha256.New, []byte("mysecret"))
	mac.Write(got.body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if got.sig != want {
		t.Errorf("signature = %q, want %q", got.sig, want)
	}
}

// TestConfigNamespace covers M12: config webhooks get their own ID space and
// are reported as config-managed, so the API can refuse to delete them with
// an explanation instead of a 404.
func TestConfigNamespace(t *testing.T) {
	d := newTestDispatcher(t, Options{})

	wh, err := d.RegisterConfig(0, models.Webhook{ID: "ignored", URL: "http://192.0.2.1/a", Events: []string{"*"}})
	if err != nil {
		t.Fatalf("RegisterConfig: %v", err)
	}
	if wh.ID != "cfg:0" {
		t.Errorf("config webhook id = %q, want cfg:0", wh.ID)
	}
	if !IsConfigManaged(wh.ID) {
		t.Error("cfg:0 not recognised as config-managed")
	}
	// The legacy hand-built form must stay recognised, otherwise an existing
	// caller's config webhooks would look deletable through the API.
	if !IsConfigManaged("cfg_0") {
		t.Error("legacy cfg_0 not recognised as config-managed")
	}
	if IsConfigManaged("wh_deadbeef") {
		t.Error("API webhook id misclassified as config-managed")
	}

	// A legacy-shaped id registered through Register is still classified as
	// config, so it cannot be deleted via the API by accident.
	_ = d.Register(models.Webhook{ID: "cfg_9", URL: "http://192.0.2.1/b", Events: []string{"*"}})
	_ = d.Register(models.Webhook{ID: "wh_abc", URL: "http://192.0.2.1/c", Events: []string{"*"}})

	sources := map[string]Source{}
	for _, reg := range d.List() {
		sources[reg.ID] = reg.Source
	}
	if sources["cfg:0"] != SourceConfig || sources["cfg_9"] != SourceConfig {
		t.Errorf("config webhooks misclassified: %v", sources)
	}
	if sources["wh_abc"] != SourceAPI {
		t.Errorf("API webhook misclassified: %v", sources)
	}
}

// TestUnregister_StopsQueuedDeliveries locks the fix for a worker that
// popped before checking its stop signal: a deleted webhook kept delivering
// its whole backlog to the removed endpoint. With a blocked receiver and a
// full queue, Unregister must stop delivery promptly rather than draining
// hours of retries into a URL the operator explicitly revoked.
func TestUnregister_StopsQueuedDeliveries(t *testing.T) {
	release := make(chan struct{})
	var delivered atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		delivered.Add(1)
		<-release // hold the first delivery open so a backlog builds
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := newTestDispatcher(t, Options{MaxAttempts: 1})
	_ = d.Register(models.Webhook{ID: "wh1", URL: srv.URL, Events: []string{"*"}})

	d.mu.RLock()
	reg := d.webhooks["wh1"]
	d.mu.RUnlock()

	for i := range 10 {
		d.Dispatch(models.Event{ID: int64(i + 1), Type: "message.received", Payload: json.RawMessage(`{}`)})
	}
	// Wait until the worker is parked inside the first delivery.
	deadline := time.Now().Add(5 * time.Second)
	for delivered.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if delivered.Load() != 1 {
		t.Fatalf("expected the worker to be parked in delivery 1, saw %d", delivered.Load())
	}

	d.Unregister("wh1")
	close(release) // let the in-flight delivery finish

	// The worker must exit after that one delivery, not drain the other 9.
	select {
	case <-reg.done:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not exit after Unregister")
	}
	if got := delivered.Load(); got != 1 {
		t.Fatalf("delivered %d events after Unregister, want 1 (only the in-flight one)", got)
	}
}
