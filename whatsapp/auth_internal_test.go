package whatsapp

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
)

const testTimeout = 2 * time.Second

func qrItem(event, code string) whatsmeow.QRChannelItem {
	return whatsmeow.QRChannelItem{Event: event, Code: code}
}

// sendOrFail sends on in and fails the test if the receiver is not draining.
// This is the whole point of the drain loop: whatsmeow's QR producer must
// never be left parked on a send.
func sendOrFail(t *testing.T, in chan<- whatsmeow.QRChannelItem, item whatsmeow.QRChannelItem) {
	t.Helper()
	select {
	case in <- item:
	case <-time.After(testTimeout):
		t.Fatalf("send of %+v blocked: bridge is not draining the QR channel", item)
	}
}

// drainUntilClosed reads out until it is closed, returning what it saw.
func drainUntilClosed(t *testing.T, out <-chan QREvent) []QREvent {
	t.Helper()
	var got []QREvent
	deadline := time.After(testTimeout)
	for {
		select {
		case evt, ok := <-out:
			if !ok {
				return got
			}
			got = append(got, evt)
		case <-deadline:
			// t.Error, not t.Fatal: this helper also runs on a non-test
			// goroutine, where Fatal's Goexit would be swallowed.
			t.Error("output channel was never closed: bridge goroutine leaked")
			return got
		}
	}
}

func waitClosed(t *testing.T, out <-chan QREvent) {
	t.Helper()
	drainUntilClosed(t, out)
}

func TestBridgeQRChannelForwardsCodesAndSuccess(t *testing.T) {
	in := make(chan whatsmeow.QRChannelItem)
	var finished atomic.Int32
	out := bridgeQRChannel(context.Background(), in, make(chan struct{}), func() { finished.Add(1) })

	var got []QREvent
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		got = drainUntilClosed(t, out)
	}()

	sendOrFail(t, in, qrItem("code", "QR-1"))
	sendOrFail(t, in, qrItem("err-client-outdated", "")) // not part of the public API
	sendOrFail(t, in, qrItem("success", ""))
	close(in)
	wg.Wait()

	want := []QREvent{{Code: "QR-1"}, {Done: true}}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if finished.Load() != 1 {
		t.Errorf("onFinish called %d times, want 1", finished.Load())
	}
}

func TestBridgeQRChannelTimeoutIsTerminal(t *testing.T) {
	in := make(chan whatsmeow.QRChannelItem)
	out := bridgeQRChannel(context.Background(), in, make(chan struct{}), nil)

	var got []QREvent
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		got = drainUntilClosed(t, out)
	}()

	sendOrFail(t, in, qrItem("timeout", ""))
	close(in)
	wg.Wait()

	if len(got) != 1 || !got[0].Done {
		t.Fatalf("got %+v, want a single Done event", got)
	}
}

// The regression this locks: the old bridge wrote to an unbuffered channel
// with a bare send, so a consumer that walked away (the REST handler
// returning early on a QR-encode error) parked the goroutine forever.
func TestBridgeQRChannelSurvivesAbandonedConsumer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	in := make(chan whatsmeow.QRChannelItem)
	var finished atomic.Bool
	out := bridgeQRChannel(ctx, in, make(chan struct{}), func() { finished.Store(true) })

	// Nobody ever reads out. The first event fills the buffer; the next one
	// would block forever without the ctx guard.
	sendOrFail(t, in, qrItem("code", "QR-1"))
	cancel()
	for i := 0; i < 5; i++ {
		sendOrFail(t, in, qrItem("code", "QR-more"))
	}
	close(in)

	waitClosed(t, out)
	if !finished.Load() {
		t.Error("onFinish not called: login slot would stay claimed forever")
	}
}

// Close must be able to release a login nobody is consuming, even when the
// caller passed a context that is never cancelled.
func TestBridgeQRChannelSurvivesClientClose(t *testing.T) {
	in := make(chan whatsmeow.QRChannelItem)
	done := make(chan struct{})
	out := bridgeQRChannel(context.Background(), in, done, nil)

	sendOrFail(t, in, qrItem("code", "QR-1")) // fills the buffer
	close(done)
	for i := 0; i < 3; i++ {
		sendOrFail(t, in, qrItem("code", "QR-more"))
	}
	close(in)

	waitClosed(t, out)
}

func TestLoginIsSingleFlight(t *testing.T) {
	c := &Client{}

	if err := c.beginLogin(); err != nil {
		t.Fatalf("first beginLogin: %v", err)
	}
	if err := c.beginLogin(); !errors.Is(err, ErrLoginInProgress) {
		t.Fatalf("second beginLogin = %v, want ErrLoginInProgress", err)
	}

	c.endLogin()
	if err := c.beginLogin(); err != nil {
		t.Fatalf("beginLogin after endLogin: %v", err)
	}
}

func TestLoginSingleFlightUnderConcurrency(t *testing.T) {
	c := &Client{}

	const n = 32
	var granted atomic.Int32
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			if err := c.beginLogin(); err == nil {
				granted.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := granted.Load(); got != 1 {
		t.Fatalf("%d concurrent logins were granted, want exactly 1", got)
	}
}

// Login must refuse before touching whatsmeow when the device is already
// paired — the zero Client has a nil whatsmeow client, so reaching it panics.
func TestLoginRejectsWhenAlreadyLoggedIn(t *testing.T) {
	c := &Client{}
	c.ident.set("31612345678:12@s.whatsapp.net", "31612345678", "Tester")

	if _, err := c.Login(context.Background()); !errors.Is(err, ErrAlreadyLoggedIn) {
		t.Fatalf("Login = %v, want ErrAlreadyLoggedIn", err)
	}
	if c.loginInFlight {
		t.Error("rejected Login left the single-flight slot claimed")
	}
}
