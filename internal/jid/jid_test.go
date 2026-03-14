package jid_test

import (
	"testing"

	"github.com/piwi3910/whatsapp-go/internal/jid"
)

func TestNormalizeJID(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain number",
			input: "1234567890",
			want:  "1234567890@s.whatsapp.net",
		},
		{
			name:  "number with plus prefix",
			input: "+1234567890",
			want:  "1234567890@s.whatsapp.net",
		},
		{
			name:  "already a user JID",
			input: "1234567890@s.whatsapp.net",
			want:  "1234567890@s.whatsapp.net",
		},
		{
			name:  "group JID",
			input: "123456789012345@g.us",
			want:  "123456789012345@g.us",
		},
		{
			name:  "number with leading whitespace",
			input: "  1234567890",
			want:  "1234567890@s.whatsapp.net",
		},
		{
			name:  "number with trailing whitespace",
			input: "1234567890  ",
			want:  "1234567890@s.whatsapp.net",
		},
		{
			name:  "international number with plus",
			input: "+4915112345678",
			want:  "4915112345678@s.whatsapp.net",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := jid.NormalizeJID(tc.input)
			if got != tc.want {
				t.Errorf("NormalizeJID(%q) = %q; want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestCompositeMessageID(t *testing.T) {
	tests := []struct {
		name       string
		chatJID    string
		senderJID  string
		waID       string
		wantLen    int
		wantStable string // expected value for a known input
	}{
		{
			name:       "basic composite",
			chatJID:    "1234567890@s.whatsapp.net",
			senderJID:  "1234567890@s.whatsapp.net",
			waID:       "ABCDEFGH1234",
			wantLen:    16,
			wantStable: "", // checked separately
		},
		{
			name:      "group message",
			chatJID:   "123456789012345@g.us",
			senderJID: "9876543210@s.whatsapp.net",
			waID:      "MSG001",
			wantLen:   16,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := jid.CompositeMessageID(tc.chatJID, tc.senderJID, tc.waID)
			if len(got) != tc.wantLen {
				t.Errorf("CompositeMessageID() len = %d; want %d (value: %q)", len(got), tc.wantLen, got)
			}
			// Verify it is hex (only 0-9, a-f).
			for _, ch := range got {
				if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f')) {
					t.Errorf("CompositeMessageID() contains non-hex char %q in %q", ch, got)
				}
			}
		})
	}

	// Stability: same inputs always produce the same output.
	t.Run("stable output", func(t *testing.T) {
		id1 := jid.CompositeMessageID("chat@s.whatsapp.net", "sender@s.whatsapp.net", "MSG1")
		id2 := jid.CompositeMessageID("chat@s.whatsapp.net", "sender@s.whatsapp.net", "MSG1")
		if id1 != id2 {
			t.Errorf("CompositeMessageID() not stable: %q != %q", id1, id2)
		}
	})

	// Different inputs must produce different outputs.
	t.Run("different inputs produce different ids", func(t *testing.T) {
		id1 := jid.CompositeMessageID("chat@s.whatsapp.net", "sender@s.whatsapp.net", "MSG1")
		id2 := jid.CompositeMessageID("chat@s.whatsapp.net", "sender@s.whatsapp.net", "MSG2")
		if id1 == id2 {
			t.Errorf("CompositeMessageID() collision for different waMessageIDs: %q", id1)
		}
	})
}
