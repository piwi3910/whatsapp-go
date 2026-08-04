package whatsapp

import (
	"encoding/json"
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"github.com/piwi3910/whatsapp-go/internal/jid"
	"github.com/piwi3910/whatsapp-go/internal/models"
)

func TestExtractMessageContent(t *testing.T) {
	tests := []struct {
		name        string
		msg         *waE2E.Message
		wantType    string
		wantContent string
		wantCaption string
	}{
		{"nil message", nil, "unknown", "", ""},
		{"empty message", &waE2E.Message{}, "unknown", "", ""},
		{
			"conversation",
			&waE2E.Message{Conversation: proto.String("hello")},
			"text", "hello", "",
		},
		{
			"extended text",
			&waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("linky")}},
			"text", "linky", "",
		},
		{
			"image with caption",
			&waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: proto.String("a cat")}},
			"image", "", "a cat",
		},
		{
			"video with caption",
			&waE2E.Message{VideoMessage: &waE2E.VideoMessage{Caption: proto.String("a clip")}},
			"video", "", "a clip",
		},
		{
			"audio",
			&waE2E.Message{AudioMessage: &waE2E.AudioMessage{}},
			"audio", "", "",
		},
		{
			"document with caption",
			&waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{Caption: proto.String("the report")}},
			"document", "", "the report",
		},
		{
			"sticker",
			&waE2E.Message{StickerMessage: &waE2E.StickerMessage{}},
			"sticker", "", "",
		},
		{
			"contact",
			&waE2E.Message{ContactMessage: &waE2E.ContactMessage{Vcard: proto.String("BEGIN:VCARD")}},
			"contact", "BEGIN:VCARD", "",
		},
		{
			"reaction",
			&waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{Text: proto.String("👍")}},
			"reaction", "👍", "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotType, gotContent, gotCaption := extractMessageContent(tt.msg)
			if gotType != tt.wantType || gotContent != tt.wantContent || gotCaption != tt.wantCaption {
				t.Errorf("extractMessageContent = (%q, %q, %q), want (%q, %q, %q)",
					gotType, gotContent, gotCaption, tt.wantType, tt.wantContent, tt.wantCaption)
			}
		})
	}
}

func TestExtractMessageContentLocationIsJSON(t *testing.T) {
	msg := &waE2E.Message{LocationMessage: &waE2E.LocationMessage{
		DegreesLatitude:  proto.Float64(52.379189),
		DegreesLongitude: proto.Float64(4.899431),
		Name:             proto.String("Amsterdam"),
	}}

	gotType, content, _ := extractMessageContent(msg)
	if gotType != "location" {
		t.Fatalf("type = %q, want %q", gotType, "location")
	}
	var decoded struct {
		Lat  float64 `json:"lat"`
		Lon  float64 `json:"lon"`
		Name string  `json:"name"`
	}
	if err := json.Unmarshal([]byte(content), &decoded); err != nil {
		t.Fatalf("location content is not JSON: %v (%q)", err, content)
	}
	if decoded.Lat != 52.379189 || decoded.Lon != 4.899431 || decoded.Name != "Amsterdam" {
		t.Errorf("decoded location = %+v", decoded)
	}
}

func TestPopulateMediaMetadata(t *testing.T) {
	key := []byte{1, 2, 3}
	msg := &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
		Mimetype:   proto.String("image/png"),
		FileLength: proto.Uint64(4096),
		DirectPath: proto.String("/v/t62/path"),
		MediaKey:   key,
	}}

	var dst models.Message
	populateMediaMetadata(&dst, msg)

	if dst.MediaType != "image/png" || dst.MediaSize != 4096 || dst.MediaURL != "/v/t62/path" || string(dst.MediaKey) != string(key) {
		t.Errorf("populateMediaMetadata gave %+v", dst)
	}

	// Non-media and nil inputs must leave the row untouched rather than panic.
	var text models.Message
	populateMediaMetadata(&text, &waE2E.Message{Conversation: proto.String("hi")})
	if text.MediaType != "" || text.MediaSize != 0 {
		t.Errorf("text message got media metadata: %+v", text)
	}
	populateMediaMetadata(nil, msg)
	populateMediaMetadata(&text, nil)
}

// M1: a read receipt for a message WE sent carries the *reader* in Sender.
// Using that as the sender component produced a composite ID matching no
// stored row, so UpdateReadStatus silently updated nothing.
func TestReceiptMessageSenders(t *testing.T) {
	own := "31612345678:12@s.whatsapp.net"
	reader := types.JID{User: "31698765432", Server: types.DefaultUserServer}
	groupSender := types.JID{User: "31655555555", Server: types.DefaultUserServer}

	t.Run("direct chat receipt uses our own JID", func(t *testing.T) {
		v := &events.Receipt{MessageSource: types.MessageSource{Sender: reader}}
		got := receiptMessageSenders(v, own)
		if len(got) != 1 || got[0] != own {
			t.Fatalf("senders = %v, want [%q]", got, own)
		}
		if contains(got, reader.String()) {
			t.Error("reader JID used as message sender; read status would never match a row")
		}
	})

	t.Run("group receipt prefers MessageSender", func(t *testing.T) {
		v := &events.Receipt{
			MessageSource: types.MessageSource{Sender: reader},
			MessageSender: groupSender,
		}
		got := receiptMessageSenders(v, own)
		if len(got) != 2 || got[0] != groupSender.String() || got[1] != own {
			t.Fatalf("senders = %v, want [%q %q]", got, groupSender, own)
		}
	})

	t.Run("no duplicate when MessageSender is us", func(t *testing.T) {
		ownJID, err := types.ParseJID(own)
		if err != nil {
			t.Fatalf("ParseJID: %v", err)
		}
		v := &events.Receipt{MessageSender: ownJID}
		got := receiptMessageSenders(v, own)
		if len(got) != 1 || got[0] != own {
			t.Fatalf("senders = %v, want [%q]", got, own)
		}
	})

	t.Run("unlinked client yields no candidates", func(t *testing.T) {
		v := &events.Receipt{MessageSource: types.MessageSource{Sender: reader}}
		if got := receiptMessageSenders(v, ""); len(got) != 0 {
			t.Fatalf("senders = %v, want none", got)
		}
	})
}

// End-to-end on the ID arithmetic: the composite ID derived from a receipt
// must equal the one the send path stored.
func TestReceiptSenderMatchesStoredOutgoingID(t *testing.T) {
	own := "31612345678:12@s.whatsapp.net"
	chat := "31698765432@s.whatsapp.net"
	waID := "3EB0ABCDEF"

	stored := jid.CompositeMessageID(chat, own, waID)

	v := &events.Receipt{
		MessageSource: types.MessageSource{
			Chat:   types.JID{User: "31698765432", Server: types.DefaultUserServer},
			Sender: types.JID{User: "31698765432", Server: types.DefaultUserServer},
		},
	}
	var matched bool
	for _, sender := range receiptMessageSenders(v, own) {
		if jid.CompositeMessageID(v.Chat.String(), sender, waID) == stored {
			matched = true
		}
	}
	if !matched {
		t.Fatal("no receipt sender candidate reproduces the stored outgoing message ID")
	}
}

func contains(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}
