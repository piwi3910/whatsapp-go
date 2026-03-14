package client

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"

	"github.com/piwi3910/whatsapp-go/internal/jid"
	"github.com/piwi3910/whatsapp-go/internal/models"
)

func (c *Client) parseJID(input string) (types.JID, error) {
	normalized, err := jid.NormalizeJID(input)
	if err != nil {
		return types.JID{}, err
	}
	return types.ParseJID(normalized)
}

// SendText sends a text message and stores it locally.
func (c *Client) SendText(jidStr, text string) (*models.SendResponse, error) {
	to, err := c.parseJID(jidStr)
	if err != nil {
		return nil, err
	}

	msg := &waE2E.Message{
		Conversation: proto.String(text),
	}

	resp, err := c.wac.SendMessage(context.Background(), to, msg)
	if err != nil {
		return nil, fmt.Errorf("sending text: %w", err)
	}

	localID := jid.CompositeMessageID(to.String(), c.wac.Store.ID.String(), resp.ID)
	now := time.Now().Unix()

	c.store.InsertMessage(&models.Message{
		ID:        localID,
		ChatJID:   to.String(),
		SenderJID: c.wac.Store.ID.String(),
		WaID:      resp.ID,
		Type:      "text",
		Content:   text,
		Timestamp: now,
		IsFromMe:  true,
	})

	return &models.SendResponse{MessageID: localID, Timestamp: now}, nil
}

// SendImage sends an image message.
func (c *Client) SendImage(jidStr string, data []byte, filename, caption string) (*models.SendResponse, error) {
	to, err := c.parseJID(jidStr)
	if err != nil {
		return nil, err
	}

	uploaded, err := c.wac.Upload(context.Background(), data, whatsmeow.MediaImage)
	if err != nil {
		return nil, fmt.Errorf("uploading image: %w", err)
	}

	msg := &waE2E.Message{
		ImageMessage: &waE2E.ImageMessage{
			URL:           proto.String(uploaded.URL),
			DirectPath:    proto.String(uploaded.DirectPath),
			MediaKey:      uploaded.MediaKey,
			FileEncSHA256: uploaded.FileEncSHA256,
			FileSHA256:    uploaded.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(data))),
			Mimetype:      proto.String(detectMIME(filename, data)),
			Caption:       proto.String(caption),
		},
	}

	return c.sendAndStore(to, msg, "image", caption, data)
}

// SendVideo sends a video message.
func (c *Client) SendVideo(jidStr string, data []byte, filename, caption string) (*models.SendResponse, error) {
	to, err := c.parseJID(jidStr)
	if err != nil {
		return nil, err
	}

	uploaded, err := c.wac.Upload(context.Background(), data, whatsmeow.MediaVideo)
	if err != nil {
		return nil, fmt.Errorf("uploading video: %w", err)
	}

	msg := &waE2E.Message{
		VideoMessage: &waE2E.VideoMessage{
			URL:           proto.String(uploaded.URL),
			DirectPath:    proto.String(uploaded.DirectPath),
			MediaKey:      uploaded.MediaKey,
			FileEncSHA256: uploaded.FileEncSHA256,
			FileSHA256:    uploaded.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(data))),
			Mimetype:      proto.String(detectMIME(filename, data)),
			Caption:       proto.String(caption),
		},
	}

	return c.sendAndStore(to, msg, "video", caption, data)
}

// SendAudio sends an audio message.
func (c *Client) SendAudio(jidStr string, data []byte, filename string) (*models.SendResponse, error) {
	to, err := c.parseJID(jidStr)
	if err != nil {
		return nil, err
	}

	uploaded, err := c.wac.Upload(context.Background(), data, whatsmeow.MediaAudio)
	if err != nil {
		return nil, fmt.Errorf("uploading audio: %w", err)
	}

	msg := &waE2E.Message{
		AudioMessage: &waE2E.AudioMessage{
			URL:           proto.String(uploaded.URL),
			DirectPath:    proto.String(uploaded.DirectPath),
			MediaKey:      uploaded.MediaKey,
			FileEncSHA256: uploaded.FileEncSHA256,
			FileSHA256:    uploaded.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(data))),
			Mimetype:      proto.String(detectMIME(filename, data)),
		},
	}

	return c.sendAndStore(to, msg, "audio", "", data)
}

// SendDocument sends a document message.
func (c *Client) SendDocument(jidStr string, data []byte, filename string) (*models.SendResponse, error) {
	to, err := c.parseJID(jidStr)
	if err != nil {
		return nil, err
	}

	uploaded, err := c.wac.Upload(context.Background(), data, whatsmeow.MediaDocument)
	if err != nil {
		return nil, fmt.Errorf("uploading document: %w", err)
	}

	msg := &waE2E.Message{
		DocumentMessage: &waE2E.DocumentMessage{
			URL:           proto.String(uploaded.URL),
			DirectPath:    proto.String(uploaded.DirectPath),
			MediaKey:      uploaded.MediaKey,
			FileEncSHA256: uploaded.FileEncSHA256,
			FileSHA256:    uploaded.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(data))),
			Mimetype:      proto.String(detectMIME(filename, data)),
			FileName:      proto.String(filename),
		},
	}

	return c.sendAndStore(to, msg, "document", "", data)
}

// SendSticker sends a sticker message.
func (c *Client) SendSticker(jidStr string, data []byte) (*models.SendResponse, error) {
	to, err := c.parseJID(jidStr)
	if err != nil {
		return nil, err
	}

	uploaded, err := c.wac.Upload(context.Background(), data, whatsmeow.MediaImage)
	if err != nil {
		return nil, fmt.Errorf("uploading sticker: %w", err)
	}

	msg := &waE2E.Message{
		StickerMessage: &waE2E.StickerMessage{
			URL:           proto.String(uploaded.URL),
			DirectPath:    proto.String(uploaded.DirectPath),
			MediaKey:      uploaded.MediaKey,
			FileEncSHA256: uploaded.FileEncSHA256,
			FileSHA256:    uploaded.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(data))),
			Mimetype:      proto.String("image/webp"),
		},
	}

	return c.sendAndStore(to, msg, "sticker", "", data)
}

// SendLocation sends a location message.
func (c *Client) SendLocation(jidStr string, lat, lon float64, name string) (*models.SendResponse, error) {
	to, err := c.parseJID(jidStr)
	if err != nil {
		return nil, err
	}

	msg := &waE2E.Message{
		LocationMessage: &waE2E.LocationMessage{
			DegreesLatitude:  proto.Float64(lat),
			DegreesLongitude: proto.Float64(lon),
			Name:             proto.String(name),
		},
	}

	return c.sendAndStore(to, msg, "location", "", nil)
}

// SendContact sends a contact card message.
func (c *Client) SendContact(jidStr, contactJIDStr string) (*models.SendResponse, error) {
	to, err := c.parseJID(jidStr)
	if err != nil {
		return nil, err
	}

	// Build a simple vCard for the contact
	vcard := fmt.Sprintf("BEGIN:VCARD\nVERSION:3.0\nTEL:%s\nEND:VCARD", contactJIDStr)

	msg := &waE2E.Message{
		ContactMessage: &waE2E.ContactMessage{
			DisplayName: proto.String(contactJIDStr),
			Vcard:       proto.String(vcard),
		},
	}

	return c.sendAndStore(to, msg, "contact", "", nil)
}

// SendReaction reacts to a message. Looks up the message in the local store
// to get the full key tuple needed by whatsmeow.
func (c *Client) SendReaction(messageID, emoji string) error {
	msg, err := c.store.GetMessage(messageID)
	if err != nil {
		return fmt.Errorf("message not found: %w", err)
	}

	chatJID, _ := types.ParseJID(msg.ChatJID)
	senderJID, _ := types.ParseJID(msg.SenderJID)

	reaction := c.wac.BuildReaction(chatJID, senderJID, msg.WaID, emoji)
	_, err = c.wac.SendMessage(context.Background(), chatJID, reaction)
	return err
}

// DeleteMessage revokes a message.
func (c *Client) DeleteMessage(messageID string, forEveryone bool) error {
	msg, err := c.store.GetMessage(messageID)
	if err != nil {
		return fmt.Errorf("message not found: %w", err)
	}

	chatJID, _ := types.ParseJID(msg.ChatJID)
	senderJID, _ := types.ParseJID(msg.SenderJID)

	if forEveryone {
		revoke := c.wac.BuildRevoke(chatJID, senderJID, msg.WaID)
		_, err = c.wac.SendMessage(context.Background(), chatJID, revoke)
		if err != nil {
			return err
		}
	}

	return c.store.DeleteMessage(messageID)
}

// MarkRead marks a message as read in both WhatsApp and local store.
func (c *Client) MarkRead(messageID string) error {
	msg, err := c.store.GetMessage(messageID)
	if err != nil {
		return fmt.Errorf("message not found: %w", err)
	}

	chatJID, _ := types.ParseJID(msg.ChatJID)
	senderJID, _ := types.ParseJID(msg.SenderJID)

	err = c.wac.MarkRead(
		context.Background(),
		[]types.MessageID{msg.WaID},
		time.Now(),
		chatJID,
		senderJID,
	)
	if err != nil {
		return err
	}

	return c.store.UpdateReadStatus(messageID, true)
}

// GetMessages retrieves messages from the local store.
func (c *Client) GetMessages(chatJID string, limit int, before int64) ([]models.Message, error) {
	return c.store.GetMessages(chatJID, limit, before)
}

// GetMessage retrieves a single message from the local store.
func (c *Client) GetMessage(messageID string) (*models.Message, error) {
	return c.store.GetMessage(messageID)
}

// sendAndStore is a helper that sends a message and stores it locally.
func (c *Client) sendAndStore(to types.JID, msg *waE2E.Message, msgType, caption string, mediaData []byte) (*models.SendResponse, error) {
	resp, err := c.wac.SendMessage(context.Background(), to, msg)
	if err != nil {
		return nil, fmt.Errorf("sending %s: %w", msgType, err)
	}

	localID := jid.CompositeMessageID(to.String(), c.wac.Store.ID.String(), resp.ID)
	now := time.Now().Unix()

	stored := &models.Message{
		ID:        localID,
		ChatJID:   to.String(),
		SenderJID: c.wac.Store.ID.String(),
		WaID:      resp.ID,
		Type:      msgType,
		Caption:   caption,
		Timestamp: now,
		IsFromMe:  true,
	}
	if mediaData != nil {
		stored.MediaSize = int64(len(mediaData))
	}
	c.store.InsertMessage(stored)

	return &models.SendResponse{MessageID: localID, Timestamp: now}, nil
}

// detectMIME detects the MIME type from filename extension, falling back to http.DetectContentType.
func detectMIME(filename string, data []byte) string {
	ext := ""
	for i := len(filename) - 1; i >= 0; i-- {
		if filename[i] == '.' {
			ext = filename[i:]
			break
		}
	}

	mimeMap := map[string]string{
		".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png",
		".gif": "image/gif", ".webp": "image/webp",
		".mp4": "video/mp4", ".3gp": "video/3gpp", ".mov": "video/quicktime",
		".mp3": "audio/mpeg", ".ogg": "audio/ogg", ".m4a": "audio/mp4", ".wav": "audio/wav",
		".pdf": "application/pdf", ".doc": "application/msword",
	}

	if mime, ok := mimeMap[ext]; ok {
		return mime
	}

	// Fallback to content detection
	if len(data) > 0 {
		return http.DetectContentType(data)
	}
	return "application/octet-stream"
}
