// cmd/wa/event.go
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/piwi3910/whatsapp-go/internal/config"
	"github.com/piwi3910/whatsapp-go/internal/models"
	"github.com/piwi3910/whatsapp-go/internal/pidfile"
)

var eventTypes string

var eventCmd = &cobra.Command{
	Use:   "event",
	Short: "Event operations",
}

var eventListenCmd = &cobra.Command{
	Use:   "listen",
	Short: "Stream events as NDJSON to stdout",
	Run: func(cmd *cobra.Command, args []string) {
		// Parse type filter
		var typeFilter map[string]bool
		if eventTypes != "" {
			typeFilter = make(map[string]bool)
			for _, t := range strings.Split(eventTypes, ",") {
				typeFilter[strings.TrimSpace(t)] = true
			}
		}

		// If a server is running, poll its event endpoint (proxy mode) — the
		// proxy client cannot register local event handlers, so the old
		// code silently streamed nothing (issue #3).
		pidPath := filepath.Join(config.Dir(), "wa.pid")
		if addr := pidfile.ServerAddress(pidPath, cfg.Server.Host, cfg.Server.Port); addr != "" && cfg.APIKey != "" {
			listenViaPolling(addr, cfg.APIKey, typeFilter)
			return
		}

		c, _, cleanup := newClient()
		defer cleanup()

		c.RegisterEventHandler(func(evt models.Event) {
			if typeFilter != nil && !typeFilter[evt.Type] {
				return
			}
			line, _ := json.Marshal(evt)
			fmt.Println(string(line))
		})

		c.SetupEventHandlers()
		if err := c.Connect(cmd.Context()); err != nil {
			exitError(err.Error(), 1)
		}

		fmt.Fprintln(os.Stderr, "Listening for events... (Ctrl+C to stop)")

		// Wait for interrupt
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		fmt.Fprintln(os.Stderr, "\nStopping.")
	},
}

// listenViaPolling streams a running server's event feed as NDJSON on stdout.
// The first request starts at after=0, so it replays the tail of the ring
// buffer (catch-up), then follows the cursor.
// debt: fixed 2s poll interval, no SSE endpoint; revisit when an SSE route
// exists, revisit condition: this function no longer needs a sleep loop.
func listenViaPolling(baseURL, apiKey string, filter map[string]bool) {
	fmt.Fprintln(os.Stderr, "Listening for events via server at "+baseURL+" (Ctrl+C to stop)")

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(stop)

	httpClient := &http.Client{Timeout: 30 * time.Second}
	after := int64(0)

	for {
		page, err := pollEventsPage(httpClient, baseURL, apiKey, after)
		switch {
		case err == nil:
			for _, evt := range page.Events {
				if filter != nil && !filter[evt.Type] {
					continue
				}
				if evt.ID > after {
					after = evt.ID
				}
				line, _ := json.Marshal(evt)
				fmt.Println(string(line))
			}
			// The page's cursor advances further than the newest printed
			// event when the filter skipped rows.
			if c, err := strconv.ParseInt(page.Cursor, 10, 64); err == nil && c > after {
				after = c
			}
		case isEventGap(err):
			// 410 EVENT_GAP: retention pruned rows below a watermark. The
			// body carries the safe cursor — resume from there.
			gap := err.(*eventsGap)
			if gap.Cursor > 0 {
				after = gap.Cursor
			}
			fmt.Fprintf(os.Stderr, "event gap (pruned through %d); resuming from %d\n", gap.PrunedThrough, after)
		default:
			// stdout stays clean NDJSON; diagnostics go to stderr.
			fmt.Fprintf(os.Stderr, "poll: %v\n", err)
		}

		select {
		case <-stop:
			fmt.Fprintln(os.Stderr, "\nStopping.")
			return
		case <-time.After(2 * time.Second):
		}
	}
}

type eventsPage struct {
	Events []models.Event `json:"events"`
	Cursor string         `json:"cursor"`
}

// eventsGap is the 410 Gone body: the caller's cursor fell below the
// retention watermark and the safe resume cursor comes back in data.
type eventsGap struct {
	Cursor        int64
	PrunedThrough int64
}

func (g *eventsGap) Error() string {
	return fmt.Sprintf("event gap: pruned through %d", g.PrunedThrough)
}

func isEventGap(err error) bool {
	_, ok := err.(*eventsGap)
	return ok
}

func pollEventsPage(client *http.Client, baseURL, apiKey string, after int64) (eventsPage, error) {
	var out eventsPage

	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/events?after=%d&limit=500", baseURL, after), nil)
	if err != nil {
		return out, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusGone {
		var gapResp models.APIResponse
		if err := json.NewDecoder(resp.Body).Decode(&gapResp); err != nil {
			return out, fmt.Errorf("decoding 410 gap response: %w", err)
		}
		data, _ := json.Marshal(gapResp.Data)
		var g eventsGap
		if err := json.Unmarshal(data, &g); err != nil {
			return out, fmt.Errorf("decoding 410 gap data: %w", err)
		}
		return out, &g
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return out, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	var apiResp models.APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return out, err
	}
	if !apiResp.OK {
		if apiResp.Error != nil {
			return out, fmt.Errorf("%s: %s", apiResp.Error.Code, apiResp.Error.Message)
		}
		return out, fmt.Errorf("request failed")
	}
	data, _ := json.Marshal(apiResp.Data)
	return out, json.Unmarshal(data, &out)
}

func init() {
	eventListenCmd.Flags().StringVar(&eventTypes, "types", "", "comma-separated event types to filter")
	eventCmd.AddCommand(eventListenCmd)
	rootCmd.AddCommand(eventCmd)
}
