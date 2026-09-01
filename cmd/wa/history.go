// cmd/wa/history.go
package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var historyCmd = &cobra.Command{
	Use:   "history",
	Short: "History operations",
}

var histCount int

var historySyncCmd = &cobra.Command{
	Use:   "sync <chat-jid>",
	Short: "Backfill past messages for a chat from the primary device",
	Long: `Requests up to --count messages immediately before the oldest locally
stored message of the chat. The messages are delivered by the primary device
and stored as they arrive, so they show up in 'wa message list' and in the
event stream like any other inbound message. A 0 count means the request
went out but nothing arrived within the wait window (the primary device is
usually the reason).`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()
		if err := c.Connect(cmd.Context()); err != nil {
			exitError(err.Error(), 1)
		}
		imported, err := c.SyncHistory(cmd.Context(), args[0], histCount)
		if err != nil {
			exitError(err.Error(), 1)
		}
		if outputFormat == "json" {
			printOutput(map[string]any{"imported": imported})
			return
		}
		if imported > 0 {
			fmt.Printf("Imported %d past message(s) from history sync.\n", imported)
		} else {
			fmt.Println("History sync requested; no new messages arrived within the wait window (your primary device may be offline).")
		}
	},
}

func init() {
	historySyncCmd.Flags().IntVar(&histCount, "count", 50, "messages to request per sync (whatsmeow recommends 50)")
	historyCmd.AddCommand(historySyncCmd)
	rootCmd.AddCommand(historyCmd)
}
