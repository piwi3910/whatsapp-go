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
		if err := c.Connect(); err != nil {
			exitError(err.Error(), 1)
		}
		contacts, err := c.GetContacts()
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
		if err := c.Connect(); err != nil {
			exitError(err.Error(), 1)
		}
		contact, err := c.GetContactInfo(args[0])
		if err != nil {
			exitError(err.Error(), 3)
		}
		printOutput(contact)
	},
}

var contactBlockCmd = &cobra.Command{
	Use:   "block <jid>",
	Short: "Block a contact",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()
		if err := c.Connect(); err != nil {
			exitError(err.Error(), 1)
		}
		if err := c.BlockContact(args[0]); err != nil {
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
		if err := c.Connect(); err != nil {
			exitError(err.Error(), 1)
		}
		if err := c.UnblockContact(args[0]); err != nil {
			exitError(err.Error(), 1)
		}
		fmt.Println("Contact unblocked.")
	},
}

func init() {
	contactCmd.AddCommand(contactListCmd, contactInfoCmd, contactBlockCmd, contactUnblockCmd)
	rootCmd.AddCommand(contactCmd)
}
