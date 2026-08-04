package jid

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

// Known WhatsApp server (domain) parts. Anything else is rejected: an
// unrecognised domain cannot be routed by whatsmeow, so accepting it only
// means the garbage surfaces later as a confusing send failure or, worse,
// as a stored row keyed by a JID that will never match a real one.
const (
	ServerUser       = "s.whatsapp.net"
	ServerLegacyUser = "c.us"
	ServerGroup      = "g.us"
	ServerHiddenUser = "lid"
	ServerBroadcast  = "broadcast"
	ServerNewsletter = "newsletter"
)

const (
	// maxJIDLen is a sanity bound, well above any real JID
	// (phone + agent + device + domain), to keep obviously bogus input
	// from reaching the parser or the database.
	maxJIDLen = 128
	// E.164 caps subscriber numbers at 15 digits; 5 is below any real
	// national number but still permits short codes used in testing.
	minPhoneLen = 5
	maxPhoneLen = 15
	// Group and newsletter IDs are numeric but longer than phone numbers
	// (e.g. 120363... newsletter/community IDs).
	maxIDLen = 32
	// Device and agent suffixes are small integers.
	maxSuffixLen = 3
)

// NormalizeJID converts flexible phone number input to a full WhatsApp JID
// and validates the result.
//
// Accepts: "+1234567890", "1234567890", "1234567890@s.whatsapp.net",
// "1234567890-1234567890@g.us", "120363000000000000@g.us", "status@broadcast".
//
// Input that is not a plausible JID is rejected rather than passed through:
// the previous behaviour returned anything containing "@" verbatim, which let
// malformed values reach types.ParseJID and get persisted into message rows.
func NormalizeJID(input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", fmt.Errorf("empty JID input")
	}
	if len(input) > maxJIDLen {
		return "", fmt.Errorf("JID too long (%d bytes, max %d)", len(input), maxJIDLen)
	}

	// Already a full JID: validate as-is (the server part is lowercased,
	// since WhatsApp domains are lowercase and comparisons elsewhere are
	// byte-exact).
	if strings.Contains(input, "@") {
		normalized, err := canonicalJID(input)
		if err != nil {
			return "", err
		}
		return normalized, nil
	}

	// Bare phone number.
	phone := strings.TrimPrefix(input, "+")
	if err := validatePhone(phone); err != nil {
		return "", err
	}
	return phone + "@" + ServerUser, nil
}

// ValidateJID reports whether s is a well-formed, fully qualified WhatsApp
// JID. It does not accept bare phone numbers — use NormalizeJID for that.
func ValidateJID(s string) error {
	_, err := canonicalJID(s)
	return err
}

// canonicalJID validates a full "user@server" JID and returns it with the
// server part lowercased.
func canonicalJID(s string) (string, error) {
	if len(s) > maxJIDLen {
		return "", fmt.Errorf("JID too long (%d bytes, max %d)", len(s), maxJIDLen)
	}
	user, server, ok := strings.Cut(s, "@")
	if !ok {
		return "", fmt.Errorf("invalid JID %q: missing '@'", s)
	}
	if strings.Contains(server, "@") {
		return "", fmt.Errorf("invalid JID %q: multiple '@'", s)
	}
	server = strings.ToLower(server)

	switch server {
	case ServerUser, ServerLegacyUser, ServerHiddenUser, ServerNewsletter, ServerGroup, ServerBroadcast:
	default:
		return "", fmt.Errorf("invalid JID %q: unknown server %q", s, server)
	}

	base, err := stripSuffixes(user)
	if err != nil {
		return "", fmt.Errorf("invalid JID %q: %w", s, err)
	}
	if base == "" {
		return "", fmt.Errorf("invalid JID %q: empty user part", s)
	}

	switch server {
	case ServerUser, ServerLegacyUser:
		if err := validatePhone(base); err != nil {
			return "", fmt.Errorf("invalid JID %q: %w", s, err)
		}
	case ServerHiddenUser, ServerNewsletter:
		// LID and newsletter IDs are opaque numeric identifiers, longer
		// than a phone number, so only the digit/length rule applies.
		if err := validateNumericID(base); err != nil {
			return "", fmt.Errorf("invalid JID %q: %w", s, err)
		}
	case ServerGroup:
		if err := validateGroupID(base); err != nil {
			return "", fmt.Errorf("invalid JID %q: %w", s, err)
		}
	case ServerBroadcast:
		// The status feed is the one non-numeric broadcast address.
		if base != "status" {
			if err := validateNumericID(base); err != nil {
				return "", fmt.Errorf("invalid JID %q: %w", s, err)
			}
		}
	}

	return user + "@" + server, nil
}

// stripSuffixes removes the optional ".agent" and ":device" suffixes that
// whatsmeow appends to fully qualified JIDs (e.g. "123.0:5@s.whatsapp.net"),
// validating that each is a small integer, and returns the base user part.
func stripSuffixes(user string) (string, error) {
	if base, device, ok := strings.Cut(user, ":"); ok {
		if err := validateSuffix(device, "device"); err != nil {
			return "", err
		}
		user = base
	}
	if base, agent, ok := strings.Cut(user, "."); ok {
		if err := validateSuffix(agent, "agent"); err != nil {
			return "", err
		}
		user = base
	}
	return user, nil
}

func validateSuffix(v, kind string) error {
	if v == "" || len(v) > maxSuffixLen || !isDigits(v) {
		return fmt.Errorf("invalid %s suffix %q", kind, v)
	}
	return nil
}

func validatePhone(phone string) error {
	if !isDigits(phone) {
		return fmt.Errorf("phone number %q must contain digits only", phone)
	}
	if len(phone) < minPhoneLen || len(phone) > maxPhoneLen {
		return fmt.Errorf("phone number %q must be %d-%d digits", phone, minPhoneLen, maxPhoneLen)
	}
	return nil
}

func validateNumericID(id string) error {
	if !isDigits(id) {
		return fmt.Errorf("identifier %q must contain digits only", id)
	}
	if len(id) > maxIDLen {
		return fmt.Errorf("identifier %q too long (max %d digits)", id, maxIDLen)
	}
	return nil
}

// validateGroupID accepts both group ID forms: the legacy
// "<creator phone>-<unix timestamp>" and the modern all-numeric ID.
func validateGroupID(id string) error {
	left, right, hasDash := strings.Cut(id, "-")
	if !hasDash {
		return validateNumericID(id)
	}
	if strings.Contains(right, "-") {
		return fmt.Errorf("group id %q has multiple '-'", id)
	}
	if err := validateNumericID(left); err != nil {
		return fmt.Errorf("group id %q: %w", id, err)
	}
	if err := validateNumericID(right); err != nil {
		return fmt.Errorf("group id %q: %w", id, err)
	}
	return nil
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// CompositeMessageID generates a deterministic 16-char local ID from the
// WhatsApp message key tuple (chatJID, senderJID, waMessageID).
func CompositeMessageID(chatJID, senderJID, waMessageID string) string {
	h := sha256.Sum256([]byte(chatJID + ":" + senderJID + ":" + waMessageID))
	return fmt.Sprintf("%x", h[:])[:16]
}
