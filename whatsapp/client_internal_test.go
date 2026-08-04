package whatsapp

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestIdentitySnapshot(t *testing.T) {
	var id identity

	if _, _, _, ok := id.snapshot(); ok {
		t.Error("zero identity reports as linked")
	}
	if _, ok := id.jidString(); ok {
		t.Error("zero identity returned a JID")
	}

	id.set("31612345678:12@s.whatsapp.net", "31612345678", "Tester")
	jidStr, user, push, ok := id.snapshot()
	if !ok || jidStr != "31612345678:12@s.whatsapp.net" || user != "31612345678" || push != "Tester" {
		t.Fatalf("snapshot = (%q, %q, %q, %v)", jidStr, user, push, ok)
	}

	// Logout clears it, and callers must see "not linked" rather than an
	// empty JID they would go on to use.
	id.set("", "", "")
	if _, ok := id.jidString(); ok {
		t.Error("cleared identity still reports as linked")
	}
}

// The race this locks: Status and the senders used to read wac.Store.ID and
// wac.Store.PushName with no synchronisation while whatsmeow rewrote them
// during pairing and Logout. Run with -race.
//
// It also pins atomicity: a reader must never observe half of a pairing
// (a JID with the wrong push name, or a "linked" state with an empty user),
// which is what would happen with independently updated fields.
func TestIdentitySnapshotIsRaceFree(t *testing.T) {
	c := &Client{}

	const (
		workers = 16
		rounds  = 500
	)
	var wg sync.WaitGroup

	// Writers: stand in for pairing and logout.
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < rounds; j++ {
				switch (i + j) % 3 {
				case 0:
					c.ident.set("31612345678:12@s.whatsapp.net", "31612345678", "Tester")
				case 1:
					c.ident.set("31698765432:3@s.whatsapp.net", "31698765432", "Other")
				default:
					c.ident.set("", "", "") // logout
				}
			}
		}(i)
	}

	// Readers: stand in for concurrent HTTP handlers and senders.
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < rounds; j++ {
				jidStr, user, push, ok := c.ident.snapshot()
				if !ok {
					if jidStr != "" || user != "" || push != "" {
						t.Errorf("unlinked snapshot carries data: %q %q %q", jidStr, user, push)
						return
					}
					continue
				}
				if user == "" || push == "" || !strings.HasPrefix(jidStr, user) {
					t.Errorf("torn identity snapshot: %q %q %q", jidStr, user, push)
					return
				}
				c.IsLoggedIn()
			}
		}()
	}

	wg.Wait()
}

func TestStatusWithoutIdentityIsLoggedOut(t *testing.T) {
	// A Client with no identity must answer without dereferencing the
	// whatsmeow client at all — here it is nil, so any such read panics.
	c := &Client{}

	if st := c.Status(); st.State != "logged_out" {
		t.Fatalf("Status().State = %q, want %q", st.State, "logged_out")
	}
	if c.IsLoggedIn() {
		t.Error("IsLoggedIn = true for an unpaired client")
	}
}

// Store.ID going nil mid-send used to make `c.wac.Store.ID.String()` a nil
// dereference. Sending while unlinked must be a clean error instead.
func TestSendWithoutIdentityReturnsError(t *testing.T) {
	c := &Client{}

	_, err := c.SendText(context.Background(), "+31612345678", "hello")
	if !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("SendText = %v, want ErrNotLoggedIn", err)
	}
}

func TestCloseIsIdempotentAndSignalsDone(t *testing.T) {
	c := &Client{done: make(chan struct{})}

	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-c.done:
	default:
		t.Fatal("Close did not signal done; owned goroutines would leak")
	}
	// A second Close must not panic on a double channel close.
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
