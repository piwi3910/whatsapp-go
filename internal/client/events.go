package client

import (
	"encoding/json"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"github.com/piwi3910/whatsapp-go/internal/jid"
	"github.com/piwi3910/whatsapp-go/internal/models"
)

// RegisterEventHandler registers a handler that receives mapped events.
func (c *Client) RegisterEventHandler(handler func(models.Event)) {
	c.handlers = append(c.handlers, handler)
}

// SetupEventHandlers registers the whatsmeow event handler that processes
// all incoming events, stores messages, and dispatches to registered handlers.
func (c *Client) SetupEventHandlers() {
	c.wac.AddEventHandler(func(evt any) {
		switch v := evt.(type) {
		case *events.Message:
			c.handleMessage(v)
		case *events.Receipt:
			c.handleReceipt(v)
		case *events.Connected:
			c.dispatch(models.Event{
				Type:      models.EventConnectionConnected,
				Payload:   "{}",
				Timestamp: time.Now().Unix(),
			})
		case *events.Disconnected:
			c.dispatch(models.Event{
				Type:      models.EventConnectionDisconnected,
				Payload:   "{}",
				Timestamp: time.Now().Unix(),
			})
		case *events.LoggedOut:
			c.dispatch(models.Event{
				Type:      models.EventConnectionLoggedOut,
				Payload:   "{}",
				Timestamp: time.Now().Unix(),
			})
		case *events.GroupInfo:
			c.handleGroupEvent(v)
		case *events.JoinedGroup:
			payload, _ := json.Marshal(map[string]string{"group_jid": v.JID.String()})
			c.dispatch(models.Event{
				Type:      models.EventGroupCreated,
				Payload:   string(payload),
				Timestamp: time.Now().Unix(),
			})
		case *events.PushName:
			payload, _ := json.Marshal(map[string]string{
				"jid": v.JID.String(), "push_name": v.NewPushName,
			})
			c.dispatch(models.Event{
				Type:      models.EventContactUpdated,
				Payload:   string(payload),
				Timestamp: time.Now().Unix(),
			})
		case *events.Presence:
			payload, _ := json.Marshal(map[string]any{
				"jid": v.From.String(), "unavailable": v.Unavailable,
			})
			c.dispatch(models.Event{
				Type:      models.EventPresenceUpdated,
				Payload:   string(payload),
				Timestamp: time.Now().Unix(),
			})
		}
	})
}

func (c *Client) handleMessage(v *events.Message) {
	info := v.Info
	chatJID := info.Chat.String()
	senderJID := info.Sender.String()
	waID := info.ID

	localID := jid.CompositeMessageID(chatJID, senderJID, waID)

	msgType, content, caption := extractMessageContent(v.Message)

	var rawProto []byte
	if v.Message != nil {
		rawProto, _ = proto.Marshal(v.Message)
	}

	msg := &models.Message{
		ID:        localID,
		ChatJID:   chatJID,
		SenderJID: senderJID,
		WaID:      waID,
		Type:      msgType,
		Content:   content,
		Caption:   caption,
		Timestamp: info.Timestamp.Unix(),
		IsFromMe:  info.IsFromMe,
		RawProto:  rawProto,
	}

	// Extract media metadata if present
	if img := v.Message.GetImageMessage(); img != nil {
		msg.MediaType = img.GetMimetype()
		msg.MediaSize = int64(img.GetFileLength())
		msg.MediaURL = img.GetDirectPath()
		msg.MediaKey = img.GetMediaKey()
	} else if vid := v.Message.GetVideoMessage(); vid != nil {
		msg.MediaType = vid.GetMimetype()
		msg.MediaSize = int64(vid.GetFileLength())
		msg.MediaURL = vid.GetDirectPath()
		msg.MediaKey = vid.GetMediaKey()
	} else if aud := v.Message.GetAudioMessage(); aud != nil {
		msg.MediaType = aud.GetMimetype()
		msg.MediaSize = int64(aud.GetFileLength())
		msg.MediaURL = aud.GetDirectPath()
		msg.MediaKey = aud.GetMediaKey()
	} else if doc := v.Message.GetDocumentMessage(); doc != nil {
		msg.MediaType = doc.GetMimetype()
		msg.MediaSize = int64(doc.GetFileLength())
		msg.MediaURL = doc.GetDirectPath()
		msg.MediaKey = doc.GetMediaKey()
	} else if stk := v.Message.GetStickerMessage(); stk != nil {
		msg.MediaType = stk.GetMimetype()
		msg.MediaSize = int64(stk.GetFileLength())
		msg.MediaURL = stk.GetDirectPath()
		msg.MediaKey = stk.GetMediaKey()
	}

	c.store.InsertMessage(msg)

	eventType := models.EventMessageReceived
	if info.IsFromMe {
		eventType = models.EventMessageSent
	}
	payload, _ := json.Marshal(msg)
	c.dispatch(models.Event{
		Type:      eventType,
		Payload:   string(payload),
		Timestamp: info.Timestamp.Unix(),
	})
}

func (c *Client) handleReceipt(v *events.Receipt) {
	if v.Type == "read" {
		for _, id := range v.MessageIDs {
			// Try to find and update in local store (best effort)
			localID := jid.CompositeMessageID(v.Chat.String(), v.Sender.String(), id)
			c.store.UpdateReadStatus(localID, true)
		}
		payload, _ := json.Marshal(map[string]any{
			"chat_jid":    v.Chat.String(),
			"message_ids": v.MessageIDs,
		})
		c.dispatch(models.Event{
			Type:      models.EventMessageRead,
			Payload:   string(payload),
			Timestamp: time.Now().Unix(),
		})
	}
}

func (c *Client) handleGroupEvent(v *events.GroupInfo) {
	if len(v.Join) > 0 {
		jids := make([]string, len(v.Join))
		for i, j := range v.Join {
			jids[i] = j.String()
		}
		payload, _ := json.Marshal(map[string]any{
			"group_jid": v.JID.String(), "participants": jids,
		})
		c.dispatch(models.Event{
			Type: models.EventGroupParticipantAdded, Payload: string(payload), Timestamp: v.Timestamp.Unix(),
		})
	}
	if len(v.Leave) > 0 {
		jids := make([]string, len(v.Leave))
		for i, j := range v.Leave {
			jids[i] = j.String()
		}
		payload, _ := json.Marshal(map[string]any{
			"group_jid": v.JID.String(), "participants": jids,
		})
		c.dispatch(models.Event{
			Type: models.EventGroupParticipantRemoved, Payload: string(payload), Timestamp: v.Timestamp.Unix(),
		})
	}
	if len(v.Promote) > 0 {
		jids := make([]string, len(v.Promote))
		for i, j := range v.Promote {
			jids[i] = j.String()
		}
		payload, _ := json.Marshal(map[string]any{
			"group_jid": v.JID.String(), "participants": jids,
		})
		c.dispatch(models.Event{
			Type: models.EventGroupParticipantPromoted, Payload: string(payload), Timestamp: v.Timestamp.Unix(),
		})
	}
	if len(v.Demote) > 0 {
		jids := make([]string, len(v.Demote))
		for i, j := range v.Demote {
			jids[i] = j.String()
		}
		payload, _ := json.Marshal(map[string]any{
			"group_jid": v.JID.String(), "participants": jids,
		})
		c.dispatch(models.Event{
			Type: models.EventGroupParticipantDemoted, Payload: string(payload), Timestamp: v.Timestamp.Unix(),
		})
	}
	if v.Name != nil || v.Topic != nil {
		payload, _ := json.Marshal(map[string]string{"group_jid": v.JID.String()})
		c.dispatch(models.Event{
			Type: models.EventGroupUpdated, Payload: string(payload), Timestamp: v.Timestamp.Unix(),
		})
	}
}

func (c *Client) dispatch(evt models.Event) {
	// Store event in DB
	c.store.InsertEvent(&evt)

	// Fan out to registered handlers
	for _, h := range c.handlers {
		h(evt)
	}
}

// extractMessageContent extracts the type, content text, and caption from a whatsmeow message.
func extractMessageContent(msg *waE2E.Message) (msgType, content, caption string) {
	switch {
	case msg.GetConversation() != "":
		return "text", msg.GetConversation(), ""
	case msg.GetExtendedTextMessage() != nil:
		return "text", msg.GetExtendedTextMessage().GetText(), ""
	case msg.GetImageMessage() != nil:
		return "image", "", msg.GetImageMessage().GetCaption()
	case msg.GetVideoMessage() != nil:
		return "video", "", msg.GetVideoMessage().GetCaption()
	case msg.GetAudioMessage() != nil:
		return "audio", "", ""
	case msg.GetDocumentMessage() != nil:
		return "document", "", msg.GetDocumentMessage().GetCaption()
	case msg.GetStickerMessage() != nil:
		return "sticker", "", ""
	case msg.GetLocationMessage() != nil:
		loc := msg.GetLocationMessage()
		locJSON, _ := json.Marshal(map[string]any{
			"lat": loc.GetDegreesLatitude(), "lon": loc.GetDegreesLongitude(),
			"name": loc.GetName(),
		})
		return "location", string(locJSON), ""
	case msg.GetContactMessage() != nil:
		return "contact", msg.GetContactMessage().GetVcard(), ""
	case msg.GetReactionMessage() != nil:
		return "reaction", msg.GetReactionMessage().GetText(), ""
	default:
		return "unknown", "", ""
	}
}
