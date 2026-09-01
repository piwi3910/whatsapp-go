package whatsapp

import (
	"context"
	"errors"
	"fmt"

	"go.mau.fi/whatsmeow"
)

// ErrAlreadyLoggedIn is returned by Login when this client already has a
// stored device pairing.
var ErrAlreadyLoggedIn = errors.New("whatsapp: already logged in")

// ErrLoginInProgress is returned by Login when a previous Login on the same
// client has not finished yet. Login is single-flight: each attempt opens a
// whatsmeow QR channel and reconnects the socket, so running two at once
// would invalidate each other's codes.
var ErrLoginInProgress = errors.New("whatsapp: login already in progress")

// Login initiates QR-based device linking. Returns a channel of QR events.
// The caller should display QR codes from the channel until Done is true.
// After Done, the caller should keep the client connected for a few seconds
// to allow whatsmeow to complete key exchange and device storage.
//
// Login is single-flight: while an attempt is running, further calls return
// ErrLoginInProgress. An attempt ends when the returned channel is closed —
// on success, on QR timeout, when ctx is cancelled, or on Close.
//
// The caller may abandon the returned channel without leaking: sends are
// buffered and guarded by ctx and by client shutdown, so the bridging
// goroutine can never park on the send forever. Cancel ctx to end an
// abandoned attempt promptly.
//
// ctx bounds the QR channel: when it is cancelled whatsmeow stops emitting
// codes, so pass a context that lives at least as long as the pairing attempt.
func (c *Client) Login(ctx context.Context) (<-chan QREvent, error) {
	if _, ok := c.ident.jidString(); ok {
		return nil, ErrAlreadyLoggedIn
	}

	if err := c.beginLogin(); err != nil {
		return nil, err
	}

	qrChan, err := c.wac.GetQRChannel(ctx)
	if err != nil {
		c.endLogin()
		return nil, fmt.Errorf("getting QR channel: %w", err)
	}

	if err := c.wac.Connect(); err != nil {
		// whatsmeow's QR goroutine is bound to ctx and to the connection;
		// releasing the single-flight guard here lets the caller retry.
		c.endLogin()
		return nil, fmt.Errorf("connecting: %w", err)
	}

	return bridgeQRChannel(ctx, qrChan, c.done, c.endLogin), nil
}

// beginLogin claims the single-flight login slot.
func (c *Client) beginLogin() error {
	c.loginMu.Lock()
	defer c.loginMu.Unlock()
	if c.loginInFlight {
		return ErrLoginInProgress
	}
	c.loginInFlight = true
	return nil
}

// endLogin releases the single-flight login slot. It is idempotent.
func (c *Client) endLogin() {
	c.loginMu.Lock()
	defer c.loginMu.Unlock()
	c.loginInFlight = false
}

// bridgeQRChannel translates whatsmeow QR items into QREvents.
//
// Two leaks are avoided here. First, the output channel is buffered and every
// send is a select over ctx and done, so a consumer that walks away (e.g. an
// HTTP handler that fails to encode the first QR code and returns) cannot
// park this goroutine forever. Second, once we stop forwarding we keep
// draining in until whatsmeow closes it, so whatsmeow's own QR producer is
// never left blocked on a send to a channel nobody reads.
func bridgeQRChannel(ctx context.Context, in <-chan whatsmeow.QRChannelItem, done <-chan struct{}, onFinish func()) <-chan QREvent {
	out := make(chan QREvent, 1)
	go func() {
		defer func() {
			close(out)
			if onFinish != nil {
				onFinish()
			}
		}()

		forwarding := true
		for item := range in {
			if !forwarding {
				continue // drain only
			}
			evt, ok := mapQRItem(item)
			if !ok {
				continue
			}
			select {
			case out <- evt:
			case <-ctx.Done():
				forwarding = false
				continue
			case <-done:
				forwarding = false
				continue
			}
			if evt.Done {
				// Terminal: nothing more is worth forwarding, but keep
				// draining so whatsmeow can finish and close in.
				forwarding = false
			}
		}
	}()
	return out
}

// mapQRItem maps a whatsmeow QR item to a QREvent. ok is false for items the
// public API does not surface.
func mapQRItem(item whatsmeow.QRChannelItem) (QREvent, bool) {
	switch item.Event {
	case "code":
		return QREvent{Code: item.Code}, true
	case "error":
		// Pairing failed (wrong phone, device limit, etc.): the channel is
		// done, and the reason must not be swallowed (issue #7).
		err := item.Error
		if err == nil {
			err = errors.New("pairing error")
		}
		return QREvent{Done: true, Err: err}, true
	case "success", "timeout":
		return QREvent{Done: true}, true
	default:
		return QREvent{}, false
	}
}

// Logout unlinks the device and clears session data.
func (c *Client) Logout(ctx context.Context) error {
	if err := c.wac.Logout(ctx); err != nil {
		return fmt.Errorf("logout: %w", err)
	}
	// whatsmeow cleared Store.ID on this goroutine, so refreshing here is
	// ordered with that write and makes senders fail cleanly rather than
	// dereference a nil ID.
	c.refreshIdentity()
	return nil
}

// Status returns the current connection and auth state.
func (c *Client) Status() ConnectionStatus {
	status := ConnectionStatus{State: "disconnected"}

	// Read the snapshot, never the whatsmeow store: Status is called from
	// HTTP handlers concurrently with pairing and logout.
	_, user, pushName, ok := c.ident.snapshot()
	if !ok {
		status.State = "logged_out"
		return status
	}

	if c.wac.IsConnected() {
		status.State = "connected"
	} else if c.wac.IsLoggedIn() {
		status.State = "connecting"
	}

	status.PhoneNumber = user
	status.PushName = pushName

	return status
}
