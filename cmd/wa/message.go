// cmd/wa/message.go
package main

import (
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"
)

var msgLimit int
var msgBefore string
var msgForEveryone bool

var messageCmd = &cobra.Command{
	Use:   "message",
	Short: "Message operations",
}

var messageListCmd = &cobra.Command{
	Use:   "list <jid>",
	Short: "List messages in a chat",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()

		var before int64
		if msgBefore != "" {
			before, _ = strconv.ParseInt(msgBefore, 10, 64)
		}

		msgs, err := c.GetMessages(args[0], msgLimit, before)
		if err != nil {
			exitError(err.Error(), 1)
		}

		if outputFormat == "json" {
			printOutput(msgs)
		} else {
			for _, m := range msgs {
				ts := time.Unix(m.Timestamp, 0).Format("2006-01-02 15:04:05")
				direction := "<-"
				if m.IsFromMe {
					direction = "->"
				}
				fmt.Printf("[%s] %s %s: %s\n", ts, direction, m.ID[:8], m.Content)
			}
		}
	},
}

var messageInfoCmd = &cobra.Command{
	Use:   "info <message-id>",
	Short: "Show message details",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()

		msg, err := c.GetMessage(args[0])
		if err != nil {
			exitError(err.Error(), 3)
		}
		printOutput(msg)
	},
}

var messageDeleteCmd = &cobra.Command{
	Use:   "delete <jid> <message-id>",
	Short: "Delete a message",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()
		if err := c.Connect(); err != nil {
			exitError(err.Error(), 1)
		}

		if err := c.DeleteMessage(args[1], msgForEveryone); err != nil {
			exitError(err.Error(), 1)
		}
		fmt.Println("Message deleted.")
	},
}

func init() {
	messageListCmd.Flags().IntVar(&msgLimit, "limit", 20, "maximum messages to return")
	messageListCmd.Flags().StringVar(&msgBefore, "before", "", "return messages before this timestamp")
	messageDeleteCmd.Flags().BoolVar(&msgForEveryone, "for-everyone", false, "delete for everyone")

	messageCmd.AddCommand(messageListCmd, messageInfoCmd, messageDeleteCmd)
	rootCmd.AddCommand(messageCmd)
}
