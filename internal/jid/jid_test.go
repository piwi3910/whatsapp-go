package jid

import "testing"

func TestNormalizeJID(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		// Accepted shapes.
		{"phone with plus", "+1234567890", "1234567890@s.whatsapp.net", false},
		{"phone without plus", "1234567890", "1234567890@s.whatsapp.net", false},
		{"phone with surrounding space", "  +1234567890 ", "1234567890@s.whatsapp.net", false},
		{"already full JID", "1234567890@s.whatsapp.net", "1234567890@s.whatsapp.net", false},
		{"full JID with device", "1234567890:12@s.whatsapp.net", "1234567890:12@s.whatsapp.net", false},
		{"full JID with agent and device", "1234567890.0:12@s.whatsapp.net", "1234567890.0:12@s.whatsapp.net", false},
		{"group JID", "123456789@g.us", "123456789@g.us", false},
		{"legacy group JID", "1234567890-1600000000@g.us", "1234567890-1600000000@g.us", false},
		{"modern group JID", "120363000000000000@g.us", "120363000000000000@g.us", false},
		{"lid JID", "98765432109876@lid", "98765432109876@lid", false},
		{"newsletter JID", "120363000000000000@newsletter", "120363000000000000@newsletter", false},
		{"status broadcast", "status@broadcast", "status@broadcast", false},
		{"uppercase server is lowercased", "1234567890@S.WhatsApp.Net", "1234567890@s.whatsapp.net", false},

		// Rejected: these all used to be passed through verbatim.
		{"empty input", "", "", true},
		{"whitespace only", "  ", "", true},
		{"non-digit phone", "not-a-number", "", true},
		{"phone too short", "123", "", true},
		{"phone too long", "1234567890123456", "", true},
		{"letters in user part", "abc@s.whatsapp.net", "", true},
		{"unknown server", "1234567890@evil.example.com", "", true},
		{"empty server", "1234567890@", "", true},
		{"empty user", "@s.whatsapp.net", "", true},
		{"multiple at signs", "123@456@s.whatsapp.net", "", true},
		{"injected newline", "1234567890@s.whatsapp.net\nfoo", "", true},
		{"injected space", "1234 567890@s.whatsapp.net", "", true},
		{"sql-ish garbage", "1'; DROP TABLE messages;--@s.whatsapp.net", "", true},
		{"non-numeric device suffix", "1234567890:xx@s.whatsapp.net", "", true},
		{"oversized device suffix", "1234567890:12345@s.whatsapp.net", "", true},
		{"group id with letters", "12345abc@g.us", "", true},
		{"group id with two dashes", "123-456-789@g.us", "", true},
		{"non-status broadcast word", "everyone@broadcast", "", true},
		{"overlong input", string(make([]byte, 200)), "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeJID(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NormalizeJID(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("NormalizeJID(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidateJID(t *testing.T) {
	// ValidateJID is the full-JID-only form: bare phone numbers are the
	// caller-friendly input NormalizeJID accepts, not a valid JID.
	if err := ValidateJID("1234567890@s.whatsapp.net"); err != nil {
		t.Errorf("ValidateJID(full JID) = %v, want nil", err)
	}
	if err := ValidateJID("1234567890"); err == nil {
		t.Error("ValidateJID(bare phone) = nil, want error")
	}
	if err := ValidateJID("1234567890@nope"); err == nil {
		t.Error("ValidateJID(unknown server) = nil, want error")
	}
}

func TestCompositeMessageID(t *testing.T) {
	id := CompositeMessageID("123@s.whatsapp.net", "456@s.whatsapp.net", "ABCDEF123")
	if len(id) != 16 {
		t.Errorf("CompositeMessageID length = %d, want 16", len(id))
	}

	// Deterministic
	id2 := CompositeMessageID("123@s.whatsapp.net", "456@s.whatsapp.net", "ABCDEF123")
	if id != id2 {
		t.Error("CompositeMessageID is not deterministic")
	}

	// Different inputs produce different output
	id3 := CompositeMessageID("789@s.whatsapp.net", "456@s.whatsapp.net", "ABCDEF123")
	if id == id3 {
		t.Error("different inputs produced same ID")
	}

	// The sender component matters — this is what makes read receipts that
	// use the wrong sender miss their row entirely.
	id4 := CompositeMessageID("123@s.whatsapp.net", "999@s.whatsapp.net", "ABCDEF123")
	if id == id4 {
		t.Error("different sender produced same ID")
	}
}
