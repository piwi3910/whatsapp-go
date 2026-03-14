package jid

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

const (
	userSuffix  = "@s.whatsapp.net"
	groupSuffix = "@g.us"
)

// NormalizeJID accepts a phone number in various formats or an existing JID
// and returns a normalized JID string.
//
// Accepted inputs:
//   - "+1234567890"      → "1234567890@s.whatsapp.net"
//   - "1234567890"       → "1234567890@s.whatsapp.net"
//   - "1234567890@s.whatsapp.net" → "1234567890@s.whatsapp.net" (unchanged)
//   - "groupid@g.us"    → "groupid@g.us" (unchanged)
func NormalizeJID(input string) string {
	input = strings.TrimSpace(input)

	// Already a full JID — return as-is.
	if strings.Contains(input, "@") {
		return input
	}

	// Strip leading '+'.
	number := strings.TrimPrefix(input, "+")

	return number + userSuffix
}

// CompositeMessageID returns the first 16 hex characters of the SHA-256 hash
// of "chatJID:senderJID:waMessageID". This produces a short, stable,
// collision-resistant identifier suitable for use as a database primary key.
func CompositeMessageID(chatJID, senderJID, waMessageID string) string {
	raw := fmt.Sprintf("%s:%s:%s", chatJID, senderJID, waMessageID)
	sum := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("%x", sum[:])[:16]
}
