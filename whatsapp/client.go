package whatsapp

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/piwi3910/whatsapp-go/internal/models"
	appstore "github.com/piwi3910/whatsapp-go/internal/store"

	_ "modernc.org/sqlite"
)

// Client wraps whatsmeow and the app store, implementing the Service interface.
type Client struct {
	wac       *whatsmeow.Client
	store     *appstore.Store
	log       waLog.Logger
	mu        sync.RWMutex
	handlers  []func(models.Event)
	ownsStore bool
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
	return &Client{
		wac:   wac,
		store: appStore,
		log:   log,
	}, nil
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

// Disconnect closes the WhatsApp connection.
func (c *Client) Disconnect() {
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
