package whatsapp

import (
	"encoding/json"
	"runtime/debug"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"github.com/piwi3910/whatsapp-go/internal/jid"
	"github.com/piwi3910/whatsapp-go/internal/models"
)

// RegisterEventHandler registers a handler that receives mapped events.
func (c *Client) RegisterEventHandler(handler func(models.Event)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handlers = append(c.handlers, handler)
}

// SetupEventHandlers registers the whatsmeow event handler that processes
// all incoming events, stores messages, and dispatches to registered handlers.
func (c *Client) SetupEventHandlers() {
	c.wac.AddEventHandler(func(evt any) {
		// whatsmeow calls this inline on its own goroutine, so an
		// unrecovered panic here takes down the whole process — and in a
		// supervised deployment, reconnects in a crash loop. Event mapping
		// touches many optional protobuf fields, which is exactly where a
		// nil deref hides.
		defer func() {
			if r := recover(); r != nil {
				c.log.Errorf("panic handling %T event: %v\n%s", evt, r, debug.Stack())
			}
		}()
		switch v := evt.(type) {
		case *events.Message:
			c.handleMessage(v)
		case *events.Receipt:
			c.handleReceipt(v)
		case *events.Connected:
			c.dispatch(models.Event{
				Type:      models.EventConnectionConnected,
				Payload:   json.RawMessage(`{}`),
				Timestamp: time.Now().Unix(),
			})
		case *events.Disconnected:
			c.dispatch(models.Event{
				Type:      models.EventConnectionDisconnected,
				Payload:   json.RawMessage(`{}`),
				Timestamp: time.Now().Unix(),
			})
		case *events.LoggedOut:
			c.dispatch(models.Event{
				Type:      models.EventConnectionLoggedOut,
				Payload:   json.RawMessage(`{}`),
				Timestamp: time.Now().Unix(),
			})
		case *events.GroupInfo:
			c.handleGroupEvent(v)
		case *events.JoinedGroup:
			payload, _ := json.Marshal(map[string]string{"group_jid": v.JID.String()})
			c.dispatch(models.Event{
				Type:      models.EventGroupCreated,
				Payload:   payload,
				Timestamp: time.Now().Unix(),
			})
		case *events.PushName:
			payload, _ := json.Marshal(map[string]string{
				"jid": v.JID.String(), "push_name": v.NewPushName,
			})
			c.dispatch(models.Event{
				Type:      models.EventContactUpdated,
				Payload:   payload,
				Timestamp: time.Now().Unix(),
			})
		case *events.Presence:
			payload, _ := json.Marshal(map[string]any{
				"jid": v.From.String(), "unavailable": v.Unavailable,
			})
			c.dispatch(models.Event{
				Type:      models.EventPresenceUpdated,
				Payload:   payload,
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

	populateMediaMetadata(msg, v.Message)

	// Never swallow this: a failure here means the message is not persisted
	// and consumers polling Events will never see it.
	if err := c.store.InsertMessage(msg); err != nil {
		c.log.Errorf("storing inbound message %s from %s: %v", msg.ID, msg.SenderJID, err)
	}

	// Determine event type — reactions and deletions get their own event types
	eventType := models.EventMessageReceived
	if info.IsFromMe {
		eventType = models.EventMessageSent
	}
	if msgType == "reaction" {
		eventType = models.EventMessageReaction
	}

	payload, _ := json.Marshal(msg)
	c.dispatch(models.Event{
		Type:      eventType,
		Payload:   payload,
		Timestamp: info.Timestamp.Unix(),
	})
}

func (c *Client) handleReceipt(v *events.Receipt) {
	// ReceiptTypeReadSelf is the same signal arriving from one of our own
	// other devices; both mean "this message has been read".
	if v.Type != types.ReceiptTypeRead && v.Type != types.ReceiptTypeReadSelf {
		return
	}

	ownJID, _ := c.ident.jidString()
	senders := receiptMessageSenders(v, ownJID)

	for _, id := range v.MessageIDs {
		for _, sender := range senders {
			localID := jid.CompositeMessageID(v.Chat.String(), sender, id)
			if err := c.store.UpdateReadStatus(localID, true); err != nil {
				c.log.Warnf("updating read status for %s: %v", localID, err)
			}
		}
	}

	payload, _ := json.Marshal(map[string]any{
		"chat_jid":    v.Chat.String(),
		"message_ids": v.MessageIDs,
	})
	c.dispatch(models.Event{
		Type:      models.EventMessageRead,
		Payload:   payload,
		Timestamp: time.Now().Unix(),
	})
}

// receiptMessageSenders returns the candidate sender components of the local
// composite message ID for a read receipt.
//
// The subtlety this fixes: v.Sender is whoever *read* the message, not who
// sent it. For the common case — someone else read a message we sent — using
// v.Sender produced a composite ID that matches no stored row, so every read
// receipt updated zero rows. The message's own sender is v.MessageSender when
// the server includes it (group reads), and otherwise this device, since a
// receipt addressed to us is by definition about a message we sent.
//
// Both candidates are tried because a "read-self" receipt from our other
// devices refers to an incoming message; updating a composite ID that does
// not exist is a harmless no-op, whereas guessing wrong loses the update.
func receiptMessageSenders(v *events.Receipt, ownJID string) []string {
	var senders []string
	if !v.MessageSender.IsEmpty() {
		senders = append(senders, v.MessageSender.String())
	}
	if ownJID != "" && (len(senders) == 0 || senders[0] != ownJID) {
		senders = append(senders, ownJID)
	}
	return senders
}

// populateMediaMetadata copies media fields out of whichever media sub-message
// is present. Shared by the inbound path and by sendAndStore so a message we
// sent is stored with the same shape as one we received.
func populateMediaMetadata(dst *models.Message, msg *waE2E.Message) {
	if dst == nil || msg == nil {
		return
	}
	switch {
	case msg.GetImageMessage() != nil:
		m := msg.GetImageMessage()
		dst.MediaType, dst.MediaSize = m.GetMimetype(), int64(m.GetFileLength())
		dst.MediaURL, dst.MediaKey = m.GetDirectPath(), m.GetMediaKey()
	case msg.GetVideoMessage() != nil:
		m := msg.GetVideoMessage()
		dst.MediaType, dst.MediaSize = m.GetMimetype(), int64(m.GetFileLength())
		dst.MediaURL, dst.MediaKey = m.GetDirectPath(), m.GetMediaKey()
	case msg.GetAudioMessage() != nil:
		m := msg.GetAudioMessage()
		dst.MediaType, dst.MediaSize = m.GetMimetype(), int64(m.GetFileLength())
		dst.MediaURL, dst.MediaKey = m.GetDirectPath(), m.GetMediaKey()
	case msg.GetDocumentMessage() != nil:
		m := msg.GetDocumentMessage()
		dst.MediaType, dst.MediaSize = m.GetMimetype(), int64(m.GetFileLength())
		dst.MediaURL, dst.MediaKey = m.GetDirectPath(), m.GetMediaKey()
	case msg.GetStickerMessage() != nil:
		m := msg.GetStickerMessage()
		dst.MediaType, dst.MediaSize = m.GetMimetype(), int64(m.GetFileLength())
		dst.MediaURL, dst.MediaKey = m.GetDirectPath(), m.GetMediaKey()
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
			Type: models.EventGroupParticipantAdded, Payload: payload, Timestamp: v.Timestamp.Unix(),
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
			Type: models.EventGroupParticipantRemoved, Payload: payload, Timestamp: v.Timestamp.Unix(),
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
			Type: models.EventGroupParticipantPromoted, Payload: payload, Timestamp: v.Timestamp.Unix(),
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
			Type: models.EventGroupParticipantDemoted, Payload: payload, Timestamp: v.Timestamp.Unix(),
		})
	}
	if v.Name != nil || v.Topic != nil {
		payload, _ := json.Marshal(map[string]string{"group_jid": v.JID.String()})
		c.dispatch(models.Event{
			Type: models.EventGroupUpdated, Payload: payload, Timestamp: v.Timestamp.Unix(),
		})
	}
}

func (c *Client) dispatch(evt models.Event) {
	// Store first, then fan out: the durable log is what a restarting
	// consumer replays, so a handler panic must not cost us the record.
	// A failure here is the difference between a consumer seeing this
	// event and never knowing it happened — say so loudly.
	if err := c.store.InsertEvent(&evt); err != nil {
		c.log.Errorf("storing %s event: %v", evt.Type, err)
	}

	// Fan out to registered handlers (read lock since handlers are append-only)
	c.mu.RLock()
	handlers := c.handlers
	c.mu.RUnlock()

	for _, h := range handlers {
		// One misbehaving handler must not kill the event loop or starve
		// the handlers registered after it.
		func() {
			defer func() {
				if r := recover(); r != nil {
					c.log.Errorf("panic in %s event handler: %v\n%s", evt.Type, r, debug.Stack())
				}
			}()
			h(evt)
		}()
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
