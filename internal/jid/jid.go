package jid

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

// NormalizeJID converts flexible phone number input to a full WhatsApp JID.
// Accepts: "+1234567890", "1234567890", "1234567890@s.whatsapp.net", "groupid@g.us"
func NormalizeJID(input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", fmt.Errorf("empty JID input")
	}
	// Already a full JID
	if strings.Contains(input, "@") {
		return input, nil
	}
	// Strip leading +
	phone := strings.TrimPrefix(input, "+")
	return phone + "@s.whatsapp.net", nil
}

// CompositeMessageID generates a deterministic 16-char local ID from the
// WhatsApp message key tuple (chatJID, senderJID, waMessageID).
func CompositeMessageID(chatJID, senderJID, waMessageID string) string {
	h := sha256.Sum256([]byte(chatJID + ":" + senderJID + ":" + waMessageID))
	return fmt.Sprintf("%x", h[:])[:16]
}
