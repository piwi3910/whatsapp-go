package client

import (
	"context"
	"fmt"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

// DownloadMedia downloads media from a message. Returns the raw bytes, MIME type, and any error.
// Reconstructs the protobuf message from raw_proto stored in DB, then uses
// whatsmeow's Download which handles decryption and hash verification.
func (c *Client) DownloadMedia(messageID string) ([]byte, string, error) {
	msg, err := c.store.GetMessage(messageID)
	if err != nil {
		return nil, "", fmt.Errorf("message not found: %w", err)
	}

	if len(msg.RawProto) == 0 {
		return nil, "", fmt.Errorf("message %q has no stored media proto", messageID)
	}

	// Reconstruct the protobuf message
	var protoMsg waE2E.Message
	if err := proto.Unmarshal(msg.RawProto, &protoMsg); err != nil {
		return nil, "", fmt.Errorf("unmarshaling proto: %w", err)
	}

	// whatsmeow.Download accepts any DownloadableMessage (ImageMessage, VideoMessage, etc.)
	// Extract the correct sub-message type
	downloadable := extractDownloadable(&protoMsg)
	if downloadable == nil {
		return nil, "", fmt.Errorf("message %q has no downloadable media", messageID)
	}

	data, err := c.wac.Download(context.Background(), downloadable)
	if err != nil {
		return nil, "", fmt.Errorf("downloading media: %w", err)
	}

	return data, msg.MediaType, nil
}

// extractDownloadable returns the DownloadableMessage from a proto message.
func extractDownloadable(msg *waE2E.Message) whatsmeow.DownloadableMessage {
	switch {
	case msg.GetImageMessage() != nil:
		return msg.GetImageMessage()
	case msg.GetVideoMessage() != nil:
		return msg.GetVideoMessage()
	case msg.GetAudioMessage() != nil:
		return msg.GetAudioMessage()
	case msg.GetDocumentMessage() != nil:
		return msg.GetDocumentMessage()
	case msg.GetStickerMessage() != nil:
		return msg.GetStickerMessage()
	default:
		return nil
	}
}
