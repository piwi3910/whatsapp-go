package whatsapp

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/piwi3910/whatsapp-go/internal/models"
	appstore "github.com/piwi3910/whatsapp-go/internal/store"

	_ "modernc.org/sqlite"
)

// identity is a mutex-guarded snapshot of the linked device's identity.
//
// whatsmeow mutates Client.Store.ID and Client.Store.PushName from its own
// goroutines (pairing, app-state sync, logout) while API handlers and senders
// read them, which is a data race — and Store.ID going nil mid-send turns
// `Store.ID.String()` into a nil dereference. Everything in this package
// therefore reads this snapshot instead of touching the whatsmeow store, and
// the snapshot is refreshed from whatsmeow's own event goroutine (see
// Client.trackIdentity).
type identity struct {
	mu       sync.RWMutex
	jid      string // full JID including device suffix; "" when logged out
	user     string // phone number part
	pushName string
}

// set records a linked identity. An empty jid means "not linked".
func (i *identity) set(jid, user, pushName string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.jid, i.user, i.pushName = jid, user, pushName
}

// snapshot returns the cached identity. ok is false when the device is not
// linked, in which case the strings are empty and callers must not attempt
// to build a JID.
func (i *identity) snapshot() (jid, user, pushName string, ok bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.jid, i.user, i.pushName, i.jid != ""
}

// jidString returns just the cached full JID, or ok=false when unlinked.
func (i *identity) jidString() (string, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.jid, i.jid != ""
}

// Client wraps whatsmeow and the app store, implementing the Service interface.
type Client struct {
	wac       *whatsmeow.Client
	store     *appstore.Store
	log       waLog.Logger
	mu        sync.RWMutex
	handlers  []func(models.Event)
	ownsStore bool

	// ident is the race-free view of the linked device (see identity).
	ident identity

	// loginMu guards loginInFlight, which makes Login single-flight.
	loginMu       sync.Mutex
	loginInFlight bool

	// done is closed by Close and unblocks any goroutine this client owns
	// (notably the QR bridge and the event worker), so shutting a Client
	// down can never leave a producer parked on a channel send forever.
	done      chan struct{}
	closeOnce sync.Once

	// eventQueue decouples the whatsmeow event loop from SQLite and handler
	// fan-out (issue #26): dispatch enqueues, a single worker stores and
	// delivers in order. One worker keeps event ordering; the bounded
	// buffer keeps a stalled store from stalling the protocol.
	eventQueue chan models.Event
	eventOnce  sync.Once
	// eventDropped counts events lost to a full queue; visible to tests.
	eventDropped int64
}

// New creates a new Client. dbPath is the SQLite database path for whatsmeow's
// device store (separate from the app store to avoid driver conflicts).
//
// New takes no context: the two calls below open and migrate the local device
// database as part of construction, and there is no in-flight operation for a
// caller to cancel — hence context.Background().
func New(appStore *appstore.Store, dbPath string, log waLog.Logger) (*Client, error) {
	container, err := sqlstore.New(context.Background(), "sqlite", "file:"+dbPath+"?_pragma=foreign_keys(on)&_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)", log)
	if err != nil {
		return nil, fmt.Errorf("creating whatsmeow container: %w", err)
	}

	deviceStore, err := container.GetFirstDevice(context.Background())
	if err != nil {
		return nil, fmt.Errorf("getting device store: %w", err)
	}

	wac := whatsmeow.NewClient(deviceStore, log)
	c := &Client{
		wac:        wac,
		store:      appStore,
		log:        log,
		done:       make(chan struct{}),
		eventQueue: make(chan models.Event, eventQueueSize),
	}

	// Seed the identity snapshot before anything can run concurrently, then
	// keep it current from whatsmeow's event goroutine. This handler is
	// registered here rather than in SetupEventHandlers because library
	// consumers are not required to call that, and a stale identity would
	// silently break sending after a pairing.
	c.refreshIdentity()
	wac.AddEventHandler(c.trackIdentity)

	return c, nil
}

// trackIdentity refreshes the cached device identity on the events that can
// change it. It runs on whatsmeow's event goroutine, which is the same place
// whatsmeow publishes those changes, so this is the only read of Store.ID /
// Store.PushName that is ordered with respect to their writes.
func (c *Client) trackIdentity(evt any) {
	switch evt.(type) {
	case *events.PairSuccess, *events.Connected, *events.LoggedOut, *events.PushNameSetting:
		c.refreshIdentity()
	}
}

// refreshIdentity copies the current whatsmeow device identity into the
// snapshot. Callers must be on a goroutine where reading the whatsmeow store
// is safe (construction, whatsmeow's event goroutine, or right after a
// Logout this goroutine performed).
func (c *Client) refreshIdentity() {
	if c.wac == nil || c.wac.Store == nil || c.wac.Store.ID == nil {
		c.ident.set("", "", "")
		return
	}
	id := c.wac.Store.ID
	c.ident.set(id.String(), id.User, c.wac.Store.PushName)
}

// Connect establishes the WhatsApp connection.
//
// ctx is accepted for API symmetry but is deliberately not forwarded to
// whatsmeow: whatsmeow's Connect uses its own long-lived background event
// context, which owns the socket and the auto-reconnect goroutine. Those must
// outlive this call, so binding them to a caller (e.g. per-request) context
// would tear the connection down as soon as the caller returned.
func (c *Client) Connect(_ context.Context) error {
	return c.wac.Connect()
}

// Disconnect closes the WhatsApp connection. It is safe to call on a client
// that never got as far as building its whatsmeow client, so that Close is
// usable on any partially constructed Client.
func (c *Client) Disconnect() {
	if c.wac == nil {
		return
	}
	c.wac.Disconnect()
}

// IsConnected returns whether the client is connected.
func (c *Client) IsConnected() bool {
	return c.wac.IsConnected()
}

// WaitForConnection blocks until the client is connected or timeout expires.
func (c *Client) WaitForConnection(timeout time.Duration) bool {
	return c.wac.WaitForConnection(timeout)
}
