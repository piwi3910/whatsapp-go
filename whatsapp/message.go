package whatsapp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
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

// ErrNotLoggedIn is returned by operations that need this device's own JID
// while the client is unpaired (or has just been logged out).
var ErrNotLoggedIn = errors.New("whatsapp: not logged in")

// SendText sends a text message and stores it locally.
func (c *Client) SendText(ctx context.Context, jidStr, text string) (*models.SendResponse, error) {
	to, err := c.parseJID(jidStr)
	if err != nil {
		return nil, err
	}

	msg := &waE2E.Message{
		Conversation: proto.String(text),
	}

	return c.sendAndStore(ctx, to, msg, "text", "", nil)
}

// SendImage sends an image message.
func (c *Client) SendImage(ctx context.Context, jidStr string, data []byte, filename, caption string) (*models.SendResponse, error) {
	to, err := c.parseJID(jidStr)
	if err != nil {
		return nil, err
	}

	uploaded, err := c.wac.Upload(ctx, data, whatsmeow.MediaImage)
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

	return c.sendAndStore(ctx, to, msg, "image", caption, data)
}

// SendVideo sends a video message.
func (c *Client) SendVideo(ctx context.Context, jidStr string, data []byte, filename, caption string) (*models.SendResponse, error) {
	to, err := c.parseJID(jidStr)
	if err != nil {
		return nil, err
	}

	uploaded, err := c.wac.Upload(ctx, data, whatsmeow.MediaVideo)
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

	return c.sendAndStore(ctx, to, msg, "video", caption, data)
}

// SendAudio sends an audio message.
func (c *Client) SendAudio(ctx context.Context, jidStr string, data []byte, filename string) (*models.SendResponse, error) {
	to, err := c.parseJID(jidStr)
	if err != nil {
		return nil, err
	}

	uploaded, err := c.wac.Upload(ctx, data, whatsmeow.MediaAudio)
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

	return c.sendAndStore(ctx, to, msg, "audio", "", data)
}

// SendDocument sends a document message.
func (c *Client) SendDocument(ctx context.Context, jidStr string, data []byte, filename string) (*models.SendResponse, error) {
	to, err := c.parseJID(jidStr)
	if err != nil {
		return nil, err
	}

	uploaded, err := c.wac.Upload(ctx, data, whatsmeow.MediaDocument)
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

	return c.sendAndStore(ctx, to, msg, "document", "", data)
}

// SendSticker sends a sticker message.
func (c *Client) SendSticker(ctx context.Context, jidStr string, data []byte) (*models.SendResponse, error) {
	to, err := c.parseJID(jidStr)
	if err != nil {
		return nil, err
	}

	uploaded, err := c.wac.Upload(ctx, data, whatsmeow.MediaImage)
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

	return c.sendAndStore(ctx, to, msg, "sticker", "", data)
}

// SendLocation sends a location message.
func (c *Client) SendLocation(ctx context.Context, jidStr string, lat, lon float64, name string) (*models.SendResponse, error) {
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

	return c.sendAndStore(ctx, to, msg, "location", "", nil)
}

// SendContact sends a contact card message.
func (c *Client) SendContact(ctx context.Context, jidStr, contactJIDStr string) (*models.SendResponse, error) {
	to, err := c.parseJID(jidStr)
	if err != nil {
		return nil, err
	}

	contact, err := c.parseJID(contactJIDStr)
	if err != nil {
		return nil, fmt.Errorf("invalid contact JID %q: %w", contactJIDStr, err)
	}

	displayName := contact.User
	vcard := buildVCard(displayName, contact.User)

	msg := &waE2E.Message{
		ContactMessage: &waE2E.ContactMessage{
			DisplayName: proto.String(displayName),
			Vcard:       proto.String(vcard),
		},
	}

	return c.sendAndStore(ctx, to, msg, "contact", "", nil)
}

// SendReaction reacts to a message. Looks up the message in the local store
// to get the full key tuple needed by whatsmeow.
func (c *Client) SendReaction(ctx context.Context, messageID, emoji string) error {
	msg, err := c.store.GetMessage(messageID)
	if err != nil {
		return fmt.Errorf("message not found: %w", err)
	}

	chatJID, _ := types.ParseJID(msg.ChatJID)
	senderJID, _ := types.ParseJID(msg.SenderJID)

	reaction := c.wac.BuildReaction(chatJID, senderJID, msg.WaID, emoji)
	_, err = c.wac.SendMessage(ctx, chatJID, reaction)
	return err
}

// DeleteMessage revokes a message.
func (c *Client) DeleteMessage(ctx context.Context, messageID string, forEveryone bool) error {
	msg, err := c.store.GetMessage(messageID)
	if err != nil {
		return fmt.Errorf("message not found: %w", err)
	}

	chatJID, _ := types.ParseJID(msg.ChatJID)
	senderJID, _ := types.ParseJID(msg.SenderJID)

	if forEveryone {
		revoke := c.wac.BuildRevoke(chatJID, senderJID, msg.WaID)
		_, err = c.wac.SendMessage(ctx, chatJID, revoke)
		if err != nil {
			return err
		}
	}

	return c.store.DeleteMessage(messageID)
}

// MarkRead marks a message as read in both WhatsApp and local store.
func (c *Client) MarkRead(ctx context.Context, messageID string) error {
	msg, err := c.store.GetMessage(messageID)
	if err != nil {
		return fmt.Errorf("message not found: %w", err)
	}

	chatJID, _ := types.ParseJID(msg.ChatJID)
	senderJID, _ := types.ParseJID(msg.SenderJID)

	err = c.wac.MarkRead(
		ctx,
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

// GetMessages retrieves messages from the local store. ctx is accepted for
// interface symmetry; the local store does not take one.
func (c *Client) GetMessages(_ context.Context, chatJID string, limit int, before int64) ([]models.Message, error) {
	return c.store.GetMessages(chatJID, limit, before)
}

// GetMessage retrieves a single message from the local store. ctx is accepted
// for interface symmetry; the local store does not take one.
func (c *Client) GetMessage(_ context.Context, messageID string) (*models.Message, error) {
	return c.store.GetMessage(messageID)
}

// sendAndStore is a helper that sends a message and stores it locally.
//
// The stored row must be complete on first write. The echo of our own message
// arrives later via events.Message with IsFromMe set, and the store's upsert
// only refreshes content and is_read — so anything missing here (notably
// raw_proto and the media metadata) stays missing forever, which is what used
// to make DownloadMedia fail for every message this client sent.
func (c *Client) sendAndStore(ctx context.Context, to types.JID, msg *waE2E.Message, msgType, caption string, mediaData []byte) (*models.SendResponse, error) {
	// Resolve our own JID before sending: it is the sender component of the
	// composite local ID, and reading it up front (from the race-free
	// snapshot) means a concurrent logout cannot turn it into a nil
	// dereference between the send and the store.
	ownJID, ok := c.ident.jidString()
	if !ok {
		return nil, ErrNotLoggedIn
	}

	resp, err := c.wac.SendMessage(ctx, to, msg)
	if err != nil {
		return nil, fmt.Errorf("sending %s: %w", msgType, err)
	}

	localID := jid.CompositeMessageID(to.String(), ownJID, resp.ID)
	now := time.Now().Unix()

	// Content is derived from the protobuf we just sent so that non-media
	// payloads (location JSON, contact vCard, text) are stored the same way
	// the inbound path would store them.
	_, content, _ := extractMessageContent(msg)

	stored := &models.Message{
		ID:        localID,
		ChatJID:   to.String(),
		SenderJID: ownJID,
		WaID:      resp.ID,
		Type:      msgType,
		Content:   content,
		Caption:   caption,
		Timestamp: now,
		IsFromMe:  true,
	}
	populateMediaMetadata(stored, msg)
	if stored.MediaSize == 0 && mediaData != nil {
		stored.MediaSize = int64(len(mediaData))
	}

	// raw_proto is what DownloadMedia reconstructs the downloadable message
	// from; without it, media we sent can never be re-downloaded.
	if rawProto, err := proto.Marshal(msg); err != nil {
		c.log.Warnf("marshaling sent %s message %s for storage: %v", msgType, localID, err)
	} else {
		stored.RawProto = rawProto
	}

	if err := c.store.InsertMessage(stored); err != nil {
		c.log.Errorf("storing sent message %s: %v", localID, err)
	}

	return &models.SendResponse{MessageID: localID, Timestamp: now}, nil
}

// buildVCard renders a minimal vCard 3.0 for a phone contact.
//
// Both values are escaped: a raw newline, ';' or ',' in a property value ends
// or splits the property, so unescaped input can inject arbitrary extra vCard
// properties into the card the recipient receives. Callers additionally
// validate the JID, so this is the second of two layers.
func buildVCard(displayName, phone string) string {
	// vCard lines are CRLF-delimited per RFC 6350 §3.2.
	return "BEGIN:VCARD\r\n" +
		"VERSION:3.0\r\n" +
		"FN:" + vcardEscape(displayName) + "\r\n" +
		"TEL;type=CELL;waid=" + vcardEscape(phone) + ":+" + vcardEscape(phone) + "\r\n" +
		"END:VCARD"
}

// vcardEscape escapes a vCard text value per RFC 6350 §3.4: backslash,
// newline, comma and semicolon are the characters with structural meaning.
// A bare CR carries no meaning of its own and is dropped rather than encoded.
func vcardEscape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			// dropped: only meaningful as part of the CRLF line break
		case ',':
			b.WriteString(`\,`)
		case ';':
			b.WriteString(`\;`)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// detectMIME detects the MIME type from filename extension, falling back to http.DetectContentType.
func detectMIME(filename string, data []byte) string {
	ext := ""
	for i := len(filename) - 1; i >= 0; i-- {
		if filename[i] == '.' {
			// Extensions arrive from user-supplied filenames, where case
			// carries no meaning ("PHOTO.JPG" is a JPEG).
			ext = strings.ToLower(filename[i:])
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
