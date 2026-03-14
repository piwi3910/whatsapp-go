package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/piwi3910/whatsapp-go/internal/api"
	"github.com/piwi3910/whatsapp-go/internal/client"
	"github.com/piwi3910/whatsapp-go/internal/config"
	"github.com/piwi3910/whatsapp-go/internal/models"
	"github.com/piwi3910/whatsapp-go/internal/pidfile"
	"github.com/piwi3910/whatsapp-go/internal/store"
	"github.com/piwi3910/whatsapp-go/internal/webhook"
)

var servePort int
var serveHost string
var serveAPIKey string

const version = "0.1.0"

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the REST API server",
	Run: func(cmd *cobra.Command, args []string) {
		// Apply flag overrides
		if serveHost != "" {
			cfg.Server.Host = serveHost
		}
		if servePort != 0 {
			cfg.Server.Port = servePort
		}
		if serveAPIKey != "" {
			cfg.APIKey = serveAPIKey
		}

		// Generate API key if not set
		if cfg.APIKey == "" {
			cfg.APIKey = config.GenerateAPIKey()
			log.Printf("Generated API key: %s", cfg.APIKey)
			if configPath != "" {
				config.Save(configPath, cfg)
			}
		}

		// Check PID file
		pidPath := filepath.Join(config.Dir(), "wa.pid")
		if pidfile.IsRunning(pidPath) {
			exitError("another server instance is already running", 1)
		}

		// Open store
		s, err := store.New(cfg.Database.Path)
		if err != nil {
			exitError(fmt.Sprintf("opening database: %v", err), 1)
		}
		defer s.Close()

		// Create client
		waLogger := waLog.Stdout("wa", "WARN", true)
		c, err := client.New(s, cfg.Database.Path, waLogger)
		if err != nil {
			exitError(fmt.Sprintf("creating client: %v", err), 1)
		}

		// Setup webhook dispatcher
		disp := webhook.New()

		webhooks, _ := s.GetWebhooks()
		for _, wh := range webhooks {
			disp.Register(wh)
		}
		for i, wh := range cfg.Webhooks {
			hook := models.Webhook{
				ID:     fmt.Sprintf("cfg_%d", i),
				URL:    wh.URL,
				Events: wh.Events,
				Secret: wh.Secret,
			}
			disp.Register(hook)
		}

		c.RegisterEventHandler(func(evt models.Event) {
			disp.Dispatch(evt)
		})
		c.SetupEventHandlers()

		// Connect to WhatsApp (if previously logged in)
		if err := c.Connect(); err != nil {
			log.Printf("WhatsApp connection: %v (login may be needed)", err)
		}

		// Write PID file
		if err := pidfile.Write(pidPath); err != nil {
			log.Printf("Warning: could not write PID file: %v", err)
		}
		defer pidfile.Remove(pidPath)

		// Context for background goroutines — cancelled on shutdown
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Media upload pruning goroutine
		go func() {
			ticker := time.NewTicker(10 * time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if n, err := s.PruneExpiredUploads(); err != nil {
						log.Printf("prune uploads: %v", err)
					} else if n > 0 {
						log.Printf("pruned %d expired media uploads", n)
					}
				}
			}
		}()

		// Event pruning goroutine
		go func() {
			ticker := time.NewTicker(5 * time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if err := s.PruneEvents(cfg.Events.MaxBuffer); err != nil {
						log.Printf("prune events: %v", err)
					}
				}
			}
		}()

		// Create API server
		srv := api.NewServer(c, s, disp, cfg.APIKey, version, cfg.Server.MaxUploadSize)

		// Graceful shutdown on signal
		go func() {
			sig := make(chan os.Signal, 1)
			signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
			<-sig
			log.Println("Shutting down...")
			cancel() // Stop background goroutines
			srv.Stop()
			c.Disconnect()
		}()

		log.Printf("API key: %s", cfg.APIKey)
		if err := srv.Start(cfg.Server.Host, cfg.Server.Port); err != nil {
			log.Printf("Server stopped: %v", err)
		}
	},
}

func init() {
	serveCmd.Flags().IntVar(&servePort, "port", 0, "server port (overrides config)")
	serveCmd.Flags().StringVar(&serveHost, "host", "", "server host (overrides config)")
	serveCmd.Flags().StringVar(&serveAPIKey, "api-key", "", "API key (overrides config)")
	rootCmd.AddCommand(serveCmd)
}
