// cmd/wa/auth.go
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mdp/qrterminal/v3"
	"github.com/spf13/cobra"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/piwi3910/whatsapp-go/internal/client"
	"github.com/piwi3910/whatsapp-go/internal/config"
	"github.com/piwi3910/whatsapp-go/internal/pidfile"
	"github.com/piwi3910/whatsapp-go/internal/store"
)

// newClient creates a client for CLI use. If a server is running (detected via
// PID file), returns a proxy client that forwards through the REST API.
// Otherwise creates a direct whatsmeow connection.
func newClient() (client.Service, *store.Store, func()) {
	pidPath := filepath.Join(config.Dir(), "wa.pid")
	serverAddr := pidfile.ServerAddress(pidPath, cfg.Server.Host, cfg.Server.Port)

	if serverAddr != "" && cfg.APIKey != "" {
		// Server is running — proxy through REST API
		proxy := newProxyClient(serverAddr, cfg.APIKey)
		return proxy, nil, func() {} // no cleanup needed
	}

	// No server running — direct connection
	s, err := store.New(cfg.Database.Path)
	if err != nil {
		exitError(fmt.Sprintf("opening database: %v", err), 1)
	}

	waDBPath := filepath.Join(filepath.Dir(cfg.Database.Path), "whatsmeow.db")
	log := waLog.Stdout("wa", "WARN", true)
	c, err := client.New(s, waDBPath, log)
	if err != nil {
		s.Close()
		exitError(fmt.Sprintf("creating client: %v", err), 1)
	}

	cleanup := func() {
		c.Disconnect()
		s.Close()
	}
	return c, s, cleanup
}

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Link a WhatsApp device via QR code",
	Run: func(cmd *cobra.Command, args []string) {
		// For login, we need the store but handle the client lifecycle manually
		// because we must keep the connection alive until pairing completes.
		s, err := store.New(cfg.Database.Path)
		if err != nil {
			exitError(fmt.Sprintf("opening database: %v", err), 1)
		}
		defer s.Close()

		waDBPath := filepath.Join(filepath.Dir(cfg.Database.Path), "whatsmeow.db")
		log := waLog.Stdout("wa", "WARN", true)
		c, err := client.New(s, waDBPath, log)
		if err != nil {
			exitError(fmt.Sprintf("creating client: %v", err), 1)
		}

		qrChan, err := c.Login()
		if err != nil {
			exitError(err.Error(), 2)
		}

		fmt.Println("Scan the QR code below with WhatsApp (Linked Devices > Link a Device):")
		fmt.Println()
		for evt := range qrChan {
			if evt.Done {
				fmt.Println("\nQR scanned! Completing pairing...")
				// Wait for whatsmeow to fully connect (handshake, key exchange, device storage)
				if c.WaitForConnection(30 * time.Second) {
					fmt.Println("Login successful!")
				} else {
					fmt.Println("Login completed (sync may still be in progress).")
				}
				// Brief pause for final DB writes
				time.Sleep(2 * time.Second)
				c.Disconnect()
				return
			}
			qrterminal.Generate(evt.Code, qrterminal.L, os.Stdout)
			fmt.Println()
		}
	},
}

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Unlink the WhatsApp device",
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()

		if err := c.Connect(); err != nil {
			exitError(err.Error(), 2)
		}

		if err := c.Logout(); err != nil {
			exitError(err.Error(), 2)
		}
		fmt.Println("Logged out successfully.")
	},
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show authentication and connection status",
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()

		status := c.Status()
		if outputFormat == "json" {
			printOutput(status)
		} else {
			fmt.Printf("State: %s\n", status.State)
			if status.PhoneNumber != "" {
				fmt.Printf("Phone: %s\n", status.PhoneNumber)
			}
			if status.PushName != "" {
				fmt.Printf("Name:  %s\n", status.PushName)
			}
		}
	},
}

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Authentication commands",
}

func init() {
	authCmd.AddCommand(authStatusCmd)
	rootCmd.AddCommand(loginCmd, logoutCmd, authCmd)
}
