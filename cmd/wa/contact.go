// cmd/wa/contact.go
package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var contactCmd = &cobra.Command{
	Use:   "contact",
	Short: "Contact operations",
}

var contactListCmd = &cobra.Command{
	Use:   "list",
	Short: "List contacts",
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()
		if err := c.Connect(cmd.Context()); err != nil {
			exitError(err.Error(), 1)
		}
		contacts, err := c.GetContacts(cmd.Context())
		if err != nil {
			exitError(err.Error(), 1)
		}
		if outputFormat == "json" {
			printOutput(contacts)
		} else {
			for _, ct := range contacts {
				name := ct.Name
				if name == "" {
					name = ct.PushName
				}
				fmt.Printf("%s  %s\n", ct.JID, name)
			}
		}
	},
}

var contactInfoCmd = &cobra.Command{
	Use:   "info <jid>",
	Short: "Show contact info",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()
		if err := c.Connect(cmd.Context()); err != nil {
			exitError(err.Error(), 1)
		}
		contact, err := c.GetContactInfo(cmd.Context(), args[0])
		if err != nil {
			exitError(err.Error(), 3)
		}
		if outputFormat == "json" {
			printOutput(contact)
		} else {
			name := contact.Name
			if name == "" {
				name = contact.PushName
			}
			fmt.Printf("%s  %s\n", contact.JID, name)
			if contact.Status != "" {
				fmt.Printf("Status: %s\n", contact.Status)
			}
		}
	},
}

var contactBlockCmd = &cobra.Command{
	Use:   "block <jid>",
	Short: "Block a contact",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()
		if err := c.Connect(cmd.Context()); err != nil {
			exitError(err.Error(), 1)
		}
		if err := c.BlockContact(cmd.Context(), args[0]); err != nil {
			exitError(err.Error(), 1)
		}
		fmt.Println("Contact blocked.")
	},
}

var contactUnblockCmd = &cobra.Command{
	Use:   "unblock <jid>",
	Short: "Unblock a contact",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()
		if err := c.Connect(cmd.Context()); err != nil {
			exitError(err.Error(), 1)
		}
		if err := c.UnblockContact(cmd.Context(), args[0]); err != nil {
			exitError(err.Error(), 1)
		}
		fmt.Println("Contact unblocked.")
	},
}

func init() {
	contactCmd.AddCommand(contactListCmd, contactInfoCmd, contactBlockCmd, contactUnblockCmd)
	rootCmd.AddCommand(contactCmd)
}
