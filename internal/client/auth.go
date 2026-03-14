package client

import (
	"context"
	"fmt"
)

// Login initiates QR-based device linking. Returns a channel of QR events.
// The caller should display QR codes from the channel until Done is true.
// After Done, the caller should keep the client connected for a few seconds
// to allow whatsmeow to complete key exchange and device storage.
func (c *Client) Login() (<-chan QREvent, error) {
	if c.wac.Store.ID != nil {
		return nil, fmt.Errorf("already logged in")
	}

	qrChan, err := c.wac.GetQRChannel(context.Background())
	if err != nil {
		return nil, fmt.Errorf("getting QR channel: %w", err)
	}

	if err := c.wac.Connect(); err != nil {
		return nil, fmt.Errorf("connecting: %w", err)
	}

	// Bridge whatsmeow QR events to our QREvent type
	out := make(chan QREvent)
	go func() {
		defer close(out)
		for evt := range qrChan {
			switch evt.Event {
			case "code":
				out <- QREvent{Code: evt.Code}
			case "success":
				out <- QREvent{Done: true}
				return
			case "timeout":
				out <- QREvent{Done: true}
				return
			}
		}
	}()

	return out, nil
}

// Logout unlinks the device and clears session data.
func (c *Client) Logout() error {
	if err := c.wac.Logout(context.Background()); err != nil {
		return fmt.Errorf("logout: %w", err)
	}
	return nil
}

// Status returns the current connection and auth state.
func (c *Client) Status() ConnectionStatus {
	status := ConnectionStatus{State: "disconnected"}

	if c.wac.Store.ID == nil {
		status.State = "logged_out"
		return status
	}

	if c.wac.IsConnected() {
		status.State = "connected"
	} else if c.wac.IsLoggedIn() {
		status.State = "connecting"
	}

	status.PhoneNumber = c.wac.Store.ID.User
	status.PushName = c.wac.Store.PushName

	return status
}
