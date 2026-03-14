// cmd/wa/event.go
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/piwi3910/whatsapp-go/internal/models"
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
		c, _, cleanup := newClient()
		defer cleanup()

		// Parse type filter
		var typeFilter map[string]bool
		if eventTypes != "" {
			typeFilter = make(map[string]bool)
			for _, t := range strings.Split(eventTypes, ",") {
				typeFilter[strings.TrimSpace(t)] = true
			}
		}

		c.RegisterEventHandler(func(evt models.Event) {
			if typeFilter != nil && !typeFilter[evt.Type] {
				return
			}
			line, _ := json.Marshal(evt)
			fmt.Println(string(line))
		})

		c.SetupEventHandlers()
		if err := c.Connect(); err != nil {
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

func init() {
	eventListenCmd.Flags().StringVar(&eventTypes, "types", "", "comma-separated event types to filter")
	eventCmd.AddCommand(eventListenCmd)
	rootCmd.AddCommand(eventCmd)
}
