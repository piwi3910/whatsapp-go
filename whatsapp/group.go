package whatsapp

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
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
//
// Accepts a bare invite code or any chat.whatsapp.com link form
// (http/https, with or without the /invite/ segment, with query or fragment).
func (c *Client) JoinGroup(ctx context.Context, inviteLink string) (string, error) {
	code, err := parseInviteCode(inviteLink)
	if err != nil {
		return "", err
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

// inviteCodePattern is the character set WhatsApp uses for invite codes
// (URL-safe base64-ish). Bounding it keeps a mistyped URL or a pasted
// sentence from being sent to the server as a "code".
var inviteCodePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{6,64}$`)

// inviteHosts are the hosts that carry group invite links.
var inviteHosts = map[string]bool{
	"chat.whatsapp.com":     true,
	"www.chat.whatsapp.com": true,
}

// parseInviteCode extracts the invite code from a group invite link.
//
// The previous implementation trimmed two literal prefixes, so the
// /invite/CODE form, query strings and fragments all ended up in the "code"
// passed to the server. Parsing the URL properly handles every documented
// shape and rejects links pointing somewhere else entirely.
func parseInviteCode(inviteLink string) (string, error) {
	s := strings.TrimSpace(inviteLink)
	if s == "" {
		return "", fmt.Errorf("empty invite link")
	}

	// A bare code has no URL structure; accept it directly.
	if !strings.Contains(s, "/") && !strings.Contains(s, "?") {
		if !inviteCodePattern.MatchString(s) {
			return "", fmt.Errorf("invalid invite code %q", inviteLink)
		}
		return s, nil
	}

	// Tolerate scheme-less links like "chat.whatsapp.com/CODE", which
	// url.Parse would otherwise read as a path.
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}

	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("invalid invite link %q: %w", inviteLink, err)
	}
	if !inviteHosts[strings.ToLower(u.Hostname())] {
		return "", fmt.Errorf("invalid invite link %q: unexpected host %q", inviteLink, u.Hostname())
	}

	// Some links carry the code as ?code=..., most carry it in the path
	// (optionally behind an "invite" segment).
	code := u.Query().Get("code")
	if code == "" {
		segments := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
		for i := len(segments) - 1; i >= 0; i-- {
			if seg := segments[i]; seg != "" && seg != "invite" {
				code = seg
				break
			}
		}
	}

	decoded, err := url.PathUnescape(code)
	if err == nil {
		code = decoded
	}
	if !inviteCodePattern.MatchString(code) {
		return "", fmt.Errorf("invalid invite link %q: no usable invite code", inviteLink)
	}
	return code, nil
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
