package whatsapp

import (
	"context"
	"fmt"
	"strings"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

// CreateGroup creates a new WhatsApp group.
func (c *Client) CreateGroup(ctx context.Context, name string, participants []string) (*models.Group, error) {
	jids := make([]types.JID, len(participants))
	for i, p := range participants {
		j, err := c.parseJID(p)
		if err != nil {
			return nil, fmt.Errorf("invalid participant %q: %w", p, err)
		}
		jids[i] = j
	}

	req := whatsmeow.ReqCreateGroup{
		Name:         name,
		Participants: jids,
	}
	info, err := c.wac.CreateGroup(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("creating group: %w", err)
	}

	return groupInfoToModel(info), nil
}

// GetGroups returns all joined groups.
func (c *Client) GetGroups(ctx context.Context) ([]models.Group, error) {
	groups, err := c.wac.GetJoinedGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting groups: %w", err)
	}

	result := make([]models.Group, len(groups))
	for i, g := range groups {
		result[i] = *groupInfoToModel(g)
	}
	return result, nil
}

// GetGroupInfo returns info about a specific group.
func (c *Client) GetGroupInfo(ctx context.Context, groupJID string) (*models.Group, error) {
	j, err := c.parseJID(groupJID)
	if err != nil {
		return nil, err
	}
	info, err := c.wac.GetGroupInfo(ctx, j)
	if err != nil {
		return nil, fmt.Errorf("getting group info: %w", err)
	}
	return groupInfoToModel(info), nil
}

// JoinGroup joins a group via invite link. Returns the group JID.
func (c *Client) JoinGroup(ctx context.Context, inviteLink string) (string, error) {
	// Extract code from link (format: https://chat.whatsapp.com/CODE)
	code := inviteLink
	if strings.HasPrefix(code, "https://chat.whatsapp.com/") {
		code = strings.TrimPrefix(code, "https://chat.whatsapp.com/")
	} else if strings.HasPrefix(code, "http://chat.whatsapp.com/") {
		code = strings.TrimPrefix(code, "http://chat.whatsapp.com/")
	}

	groupJID, err := c.wac.JoinGroupWithLink(ctx, code)
	if err != nil {
		return "", fmt.Errorf("joining group: %w", err)
	}
	return groupJID.String(), nil
}

// LeaveGroup leaves a group.
func (c *Client) LeaveGroup(ctx context.Context, groupJID string) error {
	j, err := c.parseJID(groupJID)
	if err != nil {
		return err
	}
	return c.wac.LeaveGroup(ctx, j)
}

// GetInviteLink returns the group invite link.
func (c *Client) GetInviteLink(ctx context.Context, groupJID string) (string, error) {
	j, err := c.parseJID(groupJID)
	if err != nil {
		return "", err
	}
	link, err := c.wac.GetGroupInviteLink(ctx, j, false)
	if err != nil {
		return "", fmt.Errorf("getting invite link: %w", err)
	}
	return link, nil
}

// AddParticipants adds participants to a group.
func (c *Client) AddParticipants(ctx context.Context, groupJID string, participants []string) error {
	return c.updateParticipants(ctx, groupJID, participants, whatsmeow.ParticipantChangeAdd)
}

// RemoveParticipants removes participants from a group.
func (c *Client) RemoveParticipants(ctx context.Context, groupJID string, participants []string) error {
	return c.updateParticipants(ctx, groupJID, participants, whatsmeow.ParticipantChangeRemove)
}

// PromoteParticipants makes participants group admins.
func (c *Client) PromoteParticipants(ctx context.Context, groupJID string, participants []string) error {
	return c.updateParticipants(ctx, groupJID, participants, whatsmeow.ParticipantChangePromote)
}

// DemoteParticipants removes admin status from participants.
func (c *Client) DemoteParticipants(ctx context.Context, groupJID string, participants []string) error {
	return c.updateParticipants(ctx, groupJID, participants, whatsmeow.ParticipantChangeDemote)
}

func (c *Client) updateParticipants(ctx context.Context, groupJID string, participants []string, action whatsmeow.ParticipantChange) error {
	gJID, err := c.parseJID(groupJID)
	if err != nil {
		return err
	}

	jids := make([]types.JID, len(participants))
	for i, p := range participants {
		j, err := c.parseJID(p)
		if err != nil {
			return fmt.Errorf("invalid participant %q: %w", p, err)
		}
		jids[i] = j
	}

	_, err = c.wac.UpdateGroupParticipants(ctx, gJID, jids, action)
	return err
}

func groupInfoToModel(info *types.GroupInfo) *models.Group {
	participants := make([]models.Participant, len(info.Participants))
	for i, p := range info.Participants {
		participants[i] = models.Participant{
			JID:          p.JID.String(),
			IsAdmin:      p.IsAdmin,
			IsSuperAdmin: p.IsSuperAdmin,
		}
	}
	return &models.Group{
		JID:          info.JID.String(),
		Name:         info.GroupName.Name,
		Topic:        info.GroupTopic.Topic,
		Created:      info.GroupCreated.Unix(),
		Participants: participants,
	}
}
