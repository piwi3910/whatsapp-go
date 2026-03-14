// cmd/wa/send.go
package main

import (
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/spf13/cobra"
)

var sendCaption string
var sendFilename string
var sendName string

var sendCmd = &cobra.Command{
	Use:   "send",
	Short: "Send messages",
}

var sendTextCmd = &cobra.Command{
	Use:   "text <jid> <message>",
	Short: "Send a text message",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()
		if err := c.Connect(); err != nil {
			exitError(err.Error(), 1)
		}

		text := args[1]
		if text == "-" {
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				exitError(fmt.Sprintf("reading stdin: %v", err), 1)
			}
			text = string(data)
		}

		resp, err := c.SendText(args[0], text)
		if err != nil {
			exitError(err.Error(), 1)
		}
		printOutput(resp)
	},
}

func makeSendMediaCmd(use, short, msgType string, needsCaption bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			c, _, cleanup := newClient()
			defer cleanup()
			if err := c.Connect(); err != nil {
				exitError(err.Error(), 1)
			}

			data, err := os.ReadFile(args[1])
			if err != nil {
				exitError(fmt.Sprintf("reading file: %v", err), 1)
			}

			fname := args[1]
			if sendFilename != "" {
				fname = sendFilename
			}

			var resp interface{}
			switch msgType {
			case "image":
				resp, err = c.SendImage(args[0], data, fname, sendCaption)
			case "video":
				resp, err = c.SendVideo(args[0], data, fname, sendCaption)
			case "audio":
				resp, err = c.SendAudio(args[0], data, fname)
			case "document":
				resp, err = c.SendDocument(args[0], data, fname)
			case "sticker":
				resp, err = c.SendSticker(args[0], data)
			}
			if err != nil {
				exitError(err.Error(), 1)
			}
			printOutput(resp)
		},
	}
	if needsCaption {
		cmd.Flags().StringVarP(&sendCaption, "caption", "c", "", "media caption")
	}
	return cmd
}

var sendLocationCmd = &cobra.Command{
	Use:   "location <jid> <lat> <lon>",
	Short: "Send a location",
	Args:  cobra.ExactArgs(3),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()
		if err := c.Connect(); err != nil {
			exitError(err.Error(), 1)
		}

		lat, _ := strconv.ParseFloat(args[1], 64)
		lon, _ := strconv.ParseFloat(args[2], 64)
		resp, err := c.SendLocation(args[0], lat, lon, sendName)
		if err != nil {
			exitError(err.Error(), 1)
		}
		printOutput(resp)
	},
}

var sendContactCmd = &cobra.Command{
	Use:   "contact <jid> <contact-jid>",
	Short: "Send a contact card",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()
		if err := c.Connect(); err != nil {
			exitError(err.Error(), 1)
		}

		resp, err := c.SendContact(args[0], args[1])
		if err != nil {
			exitError(err.Error(), 1)
		}
		printOutput(resp)
	},
}

var sendReactionCmd = &cobra.Command{
	Use:   "reaction <message-id> <emoji>",
	Short: "React to a message (sender looked up from local DB)",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()
		if err := c.Connect(); err != nil {
			exitError(err.Error(), 1)
		}

		if err := c.SendReaction(args[0], args[1]); err != nil {
			exitError(err.Error(), 1)
		}
		fmt.Println("Reaction sent.")
	},
}

func init() {
	sendLocationCmd.Flags().StringVarP(&sendName, "name", "n", "", "location name")

	sendCmd.AddCommand(
		sendTextCmd,
		makeSendMediaCmd("image <jid> <file>", "Send an image", "image", true),
		makeSendMediaCmd("video <jid> <file>", "Send a video", "video", true),
		makeSendMediaCmd("audio <jid> <file>", "Send audio", "audio", false),
		makeSendMediaCmd("document <jid> <file>", "Send a document", "document", false),
		makeSendMediaCmd("sticker <jid> <file>", "Send a sticker", "sticker", false),
		sendLocationCmd,
		sendContactCmd,
		sendReactionCmd,
	)
	rootCmd.AddCommand(sendCmd)
}
