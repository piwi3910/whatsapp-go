package client

import (
	"context"
	"fmt"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

// GetContacts returns all synced contacts.
func (c *Client) GetContacts() ([]models.Contact, error) {
	contacts, err := c.wac.Store.Contacts.GetAllContacts(context.Background())
	if err != nil {
		return nil, fmt.Errorf("getting contacts: %w", err)
	}

	var result []models.Contact
	for jid, info := range contacts {
		result = append(result, models.Contact{
			JID:      jid.String(),
			Name:     info.FullName,
			PushName: info.PushName,
		})
	}
	return result, nil
}

// GetContactInfo returns info about a specific contact.
func (c *Client) GetContactInfo(jidStr string) (*models.Contact, error) {
	j, err := c.parseJID(jidStr)
	if err != nil {
		return nil, err
	}

	// Get user info from WhatsApp
	users, err := c.wac.GetUserInfo(context.Background(), []types.JID{j})
	if err != nil {
		return nil, fmt.Errorf("getting user info: %w", err)
	}

	info, ok := users[j]
	if !ok {
		return nil, fmt.Errorf("contact %q not found", jidStr)
	}

	contact := &models.Contact{
		JID:       j.String(),
		Status:    info.Status,
		PictureID: info.PictureID,
	}

	// Try to get name from contact store
	stored, err := c.wac.Store.Contacts.GetContact(context.Background(), j)
	if err == nil {
		contact.Name = stored.FullName
		contact.PushName = stored.PushName
	}

	return contact, nil
}

// BlockContact blocks a contact.
func (c *Client) BlockContact(jidStr string) error {
	j, err := c.parseJID(jidStr)
	if err != nil {
		return err
	}
	_, err = c.wac.UpdateBlocklist(context.Background(), j, events.BlocklistChangeActionBlock)
	return err
}

// UnblockContact unblocks a contact.
func (c *Client) UnblockContact(jidStr string) error {
	j, err := c.parseJID(jidStr)
	if err != nil {
		return err
	}
	_, err = c.wac.UpdateBlocklist(context.Background(), j, events.BlocklistChangeActionUnblock)
	return err
}
