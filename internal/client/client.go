package client

import (
	"context"
	"fmt"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/piwi3910/whatsapp-go/internal/models"
	appstore "github.com/piwi3910/whatsapp-go/internal/store"

	_ "modernc.org/sqlite"
)

// Client wraps whatsmeow and the app store, implementing the Service interface.
type Client struct {
	wac      *whatsmeow.Client
	store    *appstore.Store
	log      waLog.Logger
	handlers []func(models.Event)
}

// New creates a new Client. dbPath is the SQLite database path used for
// whatsmeow's device store. The app store is passed separately.
func New(appStore *appstore.Store, dbPath string, log waLog.Logger) (*Client, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(on)", dbPath)
	container, err := sqlstore.New(context.Background(), "sqlite", dsn, log)
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
func (c *Client) Connect() error {
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
