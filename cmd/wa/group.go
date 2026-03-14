// cmd/wa/group.go
package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/piwi3910/whatsapp-go/internal/client"
)

var groupCmd = &cobra.Command{
	Use:   "group",
	Short: "Group operations",
}

var groupCreateCmd = &cobra.Command{
	Use:   "create <name> <jid>...",
	Short: "Create a group",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()
		if err := c.Connect(); err != nil {
			exitError(err.Error(), 1)
		}
		group, err := c.CreateGroup(args[0], args[1:])
		if err != nil {
			exitError(err.Error(), 1)
		}
		printOutput(group)
	},
}

var groupListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all groups",
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()
		if err := c.Connect(); err != nil {
			exitError(err.Error(), 1)
		}
		groups, err := c.GetGroups()
		if err != nil {
			exitError(err.Error(), 1)
		}
		if outputFormat == "json" {
			printOutput(groups)
		} else {
			for _, g := range groups {
				fmt.Printf("%s  %s  (%d members)\n", g.JID, g.Name, len(g.Participants))
			}
		}
	},
}

var groupInfoCmd = &cobra.Command{
	Use:   "info <group-jid>",
	Short: "Show group info",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()
		if err := c.Connect(); err != nil {
			exitError(err.Error(), 1)
		}
		group, err := c.GetGroupInfo(args[0])
		if err != nil {
			exitError(err.Error(), 3)
		}
		printOutput(group)
	},
}

var groupJoinCmd = &cobra.Command{
	Use:   "join <invite-link>",
	Short: "Join a group via invite link",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()
		if err := c.Connect(); err != nil {
			exitError(err.Error(), 1)
		}
		jid, err := c.JoinGroup(args[0])
		if err != nil {
			exitError(err.Error(), 1)
		}
		fmt.Printf("Joined group: %s\n", jid)
	},
}

var groupLeaveCmd = &cobra.Command{
	Use:   "leave <group-jid>",
	Short: "Leave a group",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()
		if err := c.Connect(); err != nil {
			exitError(err.Error(), 1)
		}
		if err := c.LeaveGroup(args[0]); err != nil {
			exitError(err.Error(), 1)
		}
		fmt.Println("Left group.")
	},
}

var groupInviteCmd = &cobra.Command{
	Use:   "invite <group-jid>",
	Short: "Get group invite link",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()
		if err := c.Connect(); err != nil {
			exitError(err.Error(), 1)
		}
		link, err := c.GetInviteLink(args[0])
		if err != nil {
			exitError(err.Error(), 1)
		}
		fmt.Println(link)
	},
}

func makeParticipantCmd(use, short string, action func(client.Service, string, []string) error) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.MinimumNArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			c, _, cleanup := newClient()
			defer cleanup()
			if err := c.Connect(); err != nil {
				exitError(err.Error(), 1)
			}
			if err := action(c, args[0], args[1:]); err != nil {
				exitError(err.Error(), 1)
			}
			fmt.Println("Done.")
		},
	}
}

func init() {
	groupCmd.AddCommand(
		groupCreateCmd, groupListCmd, groupInfoCmd,
		groupJoinCmd, groupLeaveCmd, groupInviteCmd,
		makeParticipantCmd("add <group-jid> <jid>...", "Add participants", func(c client.Service, gj string, p []string) error { return c.AddParticipants(gj, p) }),
		makeParticipantCmd("remove <group-jid> <jid>...", "Remove participants", func(c client.Service, gj string, p []string) error { return c.RemoveParticipants(gj, p) }),
		makeParticipantCmd("promote <group-jid> <jid>...", "Promote to admin", func(c client.Service, gj string, p []string) error { return c.PromoteParticipants(gj, p) }),
		makeParticipantCmd("demote <group-jid> <jid>...", "Demote from admin", func(c client.Service, gj string, p []string) error { return c.DemoteParticipants(gj, p) }),
	)
	rootCmd.AddCommand(groupCmd)
}
