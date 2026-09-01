package main

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/piwi3910/whatsapp-go/internal/api"
	"github.com/piwi3910/whatsapp-go/internal/config"
	"github.com/piwi3910/whatsapp-go/internal/models"
	"github.com/piwi3910/whatsapp-go/internal/pidfile"
	"github.com/piwi3910/whatsapp-go/internal/store"
	"github.com/piwi3910/whatsapp-go/internal/webhook"
	"github.com/piwi3910/whatsapp-go/whatsapp"
)

var (
	servePort       int
	serveHost       string
	serveAPIKey     string
	serveNoPidfile  bool
	serveWriteConfg bool
)

// version, commit and buildDate are reported by /api/v1/healthz and friends
// and printed by `wa version`. Vars, not consts, so a release build can
// stamp them: -ldflags "-X main.version=1.2.3 -X main.commit=abc123 -X
// main.buildDate=2026-01-01".
var version = "0.1.0"

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the REST API server",
	Run: func(cmd *cobra.Command, args []string) {
		// Flags are the last word: env > file > default is resolved in
		// config.Load, and an explicit flag beats all three.
		if serveHost != "" {
			cfg.Server.Host = serveHost
		}
		if servePort != 0 {
			cfg.Server.Port = servePort
		}
		if serveAPIKey != "" {
			cfg.APIKey = serveAPIKey
		}

		inContainer := config.InContainer()
		log := slog.With("component", "serve")

		// Generate an API key if none was supplied. The key is NOT written
		// back to disk automatically: on a read-only rootfs the write fails,
		// and in a k8s Deployment a key that only lives in the container's
		// filesystem is lost on every restart. Operators must pass WA_API_KEY
		// (from a Secret) or persist one deliberately with --write-config.
		if cfg.APIKey == "" {
			cfg.APIKey = config.GenerateAPIKey()
			// Never log the key itself: in a supervised deployment this line
			// ships the credential to the log aggregator on every start.
			log.Warn("no API key configured; generated an ephemeral one that will not survive a restart",
				"fingerprint", config.KeyFingerprint(cfg.APIKey),
				"hint", "set "+config.EnvAPIKey+" (recommended in k8s) or run once with --write-config to persist one; "+
					"until then the wa CLI cannot proxy through this server and will open the database directly")
		}
		if serveWriteConfg {
			if err := config.Save(configPath, cfg); err != nil {
				exitError(fmt.Sprintf("writing config to %s: %v", configPath, err), 1)
			}
			log.Info("wrote config", "path", configPath)
		}

		// PID file: single-instance protection for an interactive install.
		// It is actively harmful in a container — PIDs restart at 1 in a new
		// namespace, so a PID left behind on a persistent volume by a hard
		// kill will very likely match a live, unrelated process in the
		// replacement container, and the server would refuse to start
		// forever (CrashLoopBackOff). Skip it in container mode, and let
		// --no-pidfile opt out explicitly when detection guesses wrong.
		pidPath := filepath.Join(config.Dir(), "wa.pid")
		usePidfile := !serveNoPidfile && !inContainer
		if usePidfile {
			if pidfile.IsRunning(pidPath) {
				exitError("another server instance is already running", 1)
			}
		} else {
			log.Info("pidfile disabled", "container", inContainer, "no_pidfile_flag", serveNoPidfile)
		}

		// Open store
		s, err := store.New(cfg.Database.Path)
		if err != nil {
			exitError(fmt.Sprintf("opening database: %v", err), 1)
		}
		defer s.Close()

		// Create client
		waLogger := waLog.Stdout("wa", "WARN", true)
		waDBPath := filepath.Join(filepath.Dir(cfg.Database.Path), "whatsmeow.db")
		c, err := whatsapp.New(s, waDBPath, waLogger)
		if err != nil {
			exitError(fmt.Sprintf("creating client: %v", err), 1)
		}

		// Setup webhook dispatcher
		disp := webhook.NewWithPolicy(webhook.Policy{AllowPrivateTargets: cfg.AllowPrivateWebhookTargets})

		// Don't swallow this: on a store error we would start "healthy" with
		// zero persisted webhooks registered and deliver to nobody, which is
		// indistinguishable from having none configured.
		webhooks, err := s.GetWebhooks()
		if err != nil {
			exitError(fmt.Sprintf("loading webhooks: %v", err), 1)
		}
		for _, wh := range webhooks {
			disp.Register(wh)
		}
		// RegisterConfig namespaces these so they can never collide with an
		// API-created webhook, and so the API can report them as
		// config-managed (read-only) rather than pretending they don't exist.
		for i, wh := range cfg.Webhooks {
			if _, err := disp.RegisterConfig(i, models.Webhook{
				URL:    wh.URL,
				Events: wh.Events,
				Secret: wh.Secret,
			}); err != nil {
				log.Error("registering config webhook", "index", i, "error", err)
			}
		}

		c.RegisterEventHandler(func(evt models.Event) {
			disp.Dispatch(evt)
		})
		// Session-lifecycle events are worth their own log lines: they are
		// the difference between "quiet because nobody messaged us" and
		// "quiet because we are unlinked and every send is failing".
		c.RegisterEventHandler(func(evt models.Event) {
			switch evt.Type {
			case models.EventConnectionLoggedOut:
				// Do not exit. Restarting cannot re-pair the device — that
				// needs a human scanning a QR code — and an exit here turns
				// an operator-visible degraded state into an opaque restart
				// loop. Readiness now fails, which is the signal an
				// orchestrator can act on.
				log.Error("whatsapp session logged out; the device must be re-paired via POST /api/v1/auth/login",
					"event", evt.Type, "action_required", true)
			case models.EventConnectionDisconnected:
				log.Warn("whatsapp disconnected; supervisor will reconnect", "event", evt.Type)
			case models.EventConnectionConnected:
				log.Info("whatsapp connected", "event", evt.Type)
			}
		})
		c.SetupEventHandlers()

		// Context for background goroutines — cancelled on shutdown
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Connect to WhatsApp (if previously logged in). A failure here is
		// not fatal: the supervisor below keeps retrying, and readiness
		// reports the truth until it succeeds.
		if err := c.Connect(ctx); err != nil {
			log.Warn("initial whatsapp connection failed; supervisor will retry", "error", err)
		}

		go superviseConnection(ctx, c, defaultSupervisorConfig(), log.With("component", "conn-supervisor"))

		// Write PID file
		if usePidfile {
			if err := pidfile.Write(pidPath); err != nil {
				log.Warn("could not write PID file", "path", pidPath, "error", err)
			}
			defer pidfile.Remove(pidPath)
		}

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
						log.Error("prune uploads failed", "error", err)
					} else if n > 0 {
						log.Info("pruned expired media uploads", "count", n)
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
						log.Error("prune events failed", "error", err)
					}
				}
			}
		}()

		// Create API server
		srv := api.NewServer(c, s, disp, cfg.APIKey, version, cfg.Server.MaxUploadSize)

		// The signal handler ONLY stops the HTTP server. Everything else is
		// torn down below, on this goroutine, after Start returns.
		//
		// This ordering is load-bearing. http.Server.Serve returns
		// ErrServerClosed as soon as Shutdown is *called*, not when it
		// finishes — so if the teardown lived in the signal goroutine, Run
		// would return concurrently with it and the deferred store Close
		// (above) would fire while whatsmeow was still writing inbound
		// messages and while webhook deliveries were still reading events.
		// That is precisely the "store closed underneath the event handler"
		// class of silent message loss this release set out to remove.
		go func() {
			sig := make(chan os.Signal, 1)
			signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
			<-sig
			log.Info("shutting down")
			cancel() // stop background goroutines, including the supervisor
			if err := srv.Stop(); err != nil {
				log.Error("http shutdown", "error", err)
			}
		}()

		log.Info("starting api server",
			"host", cfg.Server.Host, "port", cfg.Server.Port,
			"api_key_fingerprint", config.KeyFingerprint(cfg.APIKey),
			"container", inContainer, "db", cfg.Database.Path)
		if err := srv.Start(cfg.Server.Host, cfg.Server.Port, cfg.Server.TLSCert, cfg.Server.TLSKey); err != nil {
			log.Info("server stopped", "reason", err)
		}

		// Ordered teardown, innermost dependency last: stop accepting and
		// drain webhook deliveries (they read events from the store), then
		// disconnect WhatsApp (it writes to the store), and only then let
		// the deferred s.Close() run. The drain is bounded so a wedged
		// receiver cannot hold shutdown open; whatever is still queued at
		// the deadline is dead-lettered rather than lost silently.
		// deploy/deployment.yaml budgets terminationGracePeriodSeconds for
		// this.
		dctx, dcancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := disp.Shutdown(dctx); err != nil {
			log.Warn("webhook drain incomplete", "error", err)
		}
		dcancel()
		c.Disconnect()
	},
}

// connSupervisorClient is the slice of whatsapp.Service the connection
// supervisor needs. Narrowing it keeps the retry logic testable without
// standing up a real WhatsApp client.
type connSupervisorClient interface {
	Connect(ctx context.Context) error
	IsConnected() bool
	Status() whatsapp.ConnectionStatus
}

// supervisorConfig tunes the reconnect loop. Tests shrink the delays.
type supervisorConfig struct {
	// probeInterval is how often a healthy connection is re-checked.
	probeInterval time.Duration
	// baseDelay/maxDelay bound the exponential backoff between failed
	// reconnect attempts.
	baseDelay time.Duration
	maxDelay  time.Duration
	// idleDelay is how long to wait between checks while logged out, where
	// retrying achieves nothing until a human re-pairs the device.
	idleDelay time.Duration
}

func defaultSupervisorConfig() supervisorConfig {
	return supervisorConfig{
		probeInterval: 15 * time.Second,
		baseDelay:     time.Second,
		maxDelay:      60 * time.Second,
		idleDelay:     30 * time.Second,
	}
}

// superviseConnection keeps the WhatsApp session up for the lifetime of ctx.
//
// whatsmeow reconnects on its own in the common cases, but not all of them:
// a connection that never came up (initial Connect failed) and some terminal
// socket errors leave the client permanently disconnected while the process
// keeps happily serving HTTP. This loop closes that gap — while the client is
// logged in but not connected it retries Connect with jittered exponential
// backoff, capped, until ctx is cancelled. Jitter matters because every
// replica of a restarted deployment would otherwise retry in lockstep.
//
// It deliberately does nothing when the session is logged out: re-pairing
// requires a QR scan, so retrying only burns cycles. Readiness reports the
// state instead.
func superviseConnection(ctx context.Context, c connSupervisorClient, cfg supervisorConfig, log *slog.Logger) {
	backoff := cfg.baseDelay
	delay := cfg.probeInterval

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}

		switch state := c.Status().State; {
		case state == "logged_out":
			backoff = cfg.baseDelay
			delay = cfg.idleDelay
		case c.IsConnected():
			backoff = cfg.baseDelay
			delay = cfg.probeInterval
		default:
			if err := c.Connect(ctx); err != nil {
				delay = jitter(backoff)
				log.Warn("reconnect failed", "error", err, "state", state, "retry_in", delay.String())
				backoff = min(backoff*2, cfg.maxDelay)
			} else {
				log.Info("reconnected to whatsapp")
				backoff = cfg.baseDelay
				delay = cfg.probeInterval
			}
		}
	}
}

// jitter spreads retries over [d/2, d) so restarted replicas do not
// synchronise into a thundering herd against WhatsApp's servers.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	half := d / 2
	return half + time.Duration(rand.Int64N(int64(half)+1))
}

func init() {
	serveCmd.Flags().IntVar(&servePort, "port", 0, "server port (overrides config)")
	serveCmd.Flags().StringVar(&serveHost, "host", "", "server host (overrides config)")
	serveCmd.Flags().StringVar(&serveAPIKey, "api-key", "", "API key (overrides config)")
	serveCmd.Flags().BoolVar(&serveNoPidfile, "no-pidfile", false, "skip the single-instance PID file (implied in container mode)")
	serveCmd.Flags().BoolVar(&serveWriteConfg, "write-config", false, "write the effective configuration to the config path and continue")
	rootCmd.AddCommand(serveCmd)
}
