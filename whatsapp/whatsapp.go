// Package whatsapp is the public Go API for whatsapp-go: a library for
// sending and receiving WhatsApp messages over the multi-device protocol
// (via whatsmeow), with local persistence of messages and an event log.
//
// It is the same engine the `wa` CLI and REST server are built on — those
// are thin layers over this package.
//
// # Quick start
//
//	c, err := whatsapp.Open(whatsapp.Options{StateDir: "/var/lib/wa"})
//	if err != nil {
//		return err
//	}
//	defer c.Close()
//
//	// First run only: pair by scanning the QR code.
//	if !c.IsLoggedIn() {
//		qr, err := c.Login(ctx)
//		if err != nil {
//			return err
//		}
//		for evt := range qr {
//			if evt.Done {
//				break
//			}
//			fmt.Println("scan this:", evt.Code)
//		}
//	}
//
//	c.OnEvent(func(e whatsapp.Event) {
//		log.Printf("%s: %s", e.Type, e.Payload)
//	})
//	if err := c.Connect(ctx); err != nil {
//		return err
//	}
//	_, err = c.SendText(ctx, "+31612345678", "hello from Go")
//
// # Persistence
//
// A Client keeps two SQLite databases under Options.StateDir: the whatsmeow
// device/session store (holds the pairing — deleting it forces a re-scan)
// and the application store (messages and the event log). Both are local
// files, so a Client is single-instance by nature: run one process per
// linked WhatsApp account.
//
// # Receiving messages
//
// Either register a handler with OnEvent for push delivery in-process, or
// poll Events for a durable, cursor-based feed that survives restarts.
// Events is the right choice when the consumer must not miss messages
// across its own downtime.
package whatsapp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/piwi3910/whatsapp-go/internal/models"
	appstore "github.com/piwi3910/whatsapp-go/internal/store"
)

// Public data types. These are aliases, so values returned by this package
// can be used directly without importing anything else.
type (
	// Message is a single WhatsApp message, inbound or outbound.
	Message = models.Message
	// Event is one entry in the durable event log (see Client.Events).
	Event = models.Event
	// Contact is a WhatsApp contact.
	Contact = models.Contact
	// Group is a WhatsApp group and its participants.
	Group = models.Group
	// Participant is one member of a Group.
	Participant = models.Participant
	// SendResponse identifies a message this client sent.
	SendResponse = models.SendResponse
)

// Event type constants, as they appear in Event.Type.
const (
	EventMessageReceived = "message.received"
	EventMessageSent     = "message.sent"
	EventReceipt         = "message.receipt"
	EventConnected       = "connection.connected"
	EventDisconnected    = "connection.disconnected"
	EventLoggedOut       = "connection.logged_out"
)

// Options configures a Client.
type Options struct {
	// StateDir is the directory holding the two SQLite databases (device
	// pairing and message/event store). It is created if absent. Required
	// unless both DevicePath and StorePath are set.
	//
	// Treat this directory as durable state: losing it unlinks the device
	// and requires a new QR scan.
	StateDir string

	// DevicePath overrides the whatsmeow device-store path.
	// Defaults to StateDir/whatsmeow.db.
	DevicePath string

	// StorePath overrides the application store (messages, events) path.
	// Defaults to StateDir/wa.db.
	StorePath string

	// Logger receives whatsmeow's protocol logs. Defaults to a no-op
	// logger; pass waLog.Stdout("Client", "INFO", true) to see them.
	Logger waLog.Logger
}

func (o *Options) applyDefaults() error {
	if o.Logger == nil {
		o.Logger = waLog.Noop
	}
	if o.DevicePath == "" || o.StorePath == "" {
		if o.StateDir == "" {
			return fmt.Errorf("whatsapp: Options.StateDir is required (or set both DevicePath and StorePath)")
		}
		if err := os.MkdirAll(o.StateDir, 0o700); err != nil {
			return fmt.Errorf("whatsapp: creating state dir: %w", err)
		}
	}
	if o.DevicePath == "" {
		o.DevicePath = filepath.Join(o.StateDir, "whatsmeow.db")
	}
	if o.StorePath == "" {
		o.StorePath = filepath.Join(o.StateDir, "wa.db")
	}
	return nil
}

// Open creates a Client with its own persistent stores. Call Close when
// done. Open does not connect — call Connect (and Login first, if this
// account has never been paired).
func Open(opts Options) (*Client, error) {
	if err := opts.applyDefaults(); err != nil {
		return nil, err
	}
	st, err := appstore.New(opts.StorePath)
	if err != nil {
		return nil, fmt.Errorf("whatsapp: opening message store: %w", err)
	}
	c, err := New(st, opts.DevicePath, opts.Logger)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	c.ownsStore = true
	return c, nil
}

// Close disconnects the client and releases its stores. It is safe to call
// on a Client that was never connected, and safe to call more than once.
func (c *Client) Close() error {
	// Signal goroutines this client owns (the QR bridge) before tearing the
	// connection down, so an abandoned login cannot outlive the Client.
	c.closeOnce.Do(func() {
		if c.done != nil {
			close(c.done)
		}
	})
	c.Disconnect()
	if c.ownsStore && c.store != nil {
		return c.store.Close()
	}
	return nil
}

// IsLoggedIn reports whether this client has a stored device pairing. When
// false, Login must complete a QR scan before Connect can succeed.
func (c *Client) IsLoggedIn() bool {
	// Uses the identity snapshot rather than whatsmeow's store, which is
	// mutated concurrently by pairing and logout.
	_, ok := c.ident.jidString()
	return ok
}

// OnEvent registers a handler invoked for every event (inbound messages,
// receipts, connection changes). Handlers run on the whatsmeow event
// goroutine: keep them fast and non-blocking, and do not call back into
// Client methods that send, or you risk stalling event delivery.
//
// A handler that panics is recovered and logged rather than taking down
// the process. For delivery that survives restarts, use Events instead.
func (c *Client) OnEvent(handler func(Event)) {
	c.RegisterEventHandler(handler)
}

// Events returns entries from the durable event log with ID greater than
// after, oldest first, up to limit entries. Passing after=0 starts from the
// beginning of the retained log.
//
// This is the restart-safe way to consume inbound messages: persist the ID
// of the last event you processed and pass it as after next time. Delivery
// is at-least-once — the same WhatsApp message can appear more than once
// after a reconnect or history sync, so consumers must deduplicate on the
// message ID carried in the payload.
func (c *Client) Events(_ context.Context, after int64, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 50
	}
	return c.store.GetEvents(after, limit)
}
