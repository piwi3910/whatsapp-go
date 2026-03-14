package jid

import "testing"

func TestNormalizeJID(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"phone with plus", "+1234567890", "1234567890@s.whatsapp.net", false},
		{"phone without plus", "1234567890", "1234567890@s.whatsapp.net", false},
		{"already full JID", "1234567890@s.whatsapp.net", "1234567890@s.whatsapp.net", false},
		{"group JID", "123456789@g.us", "123456789@g.us", false},
		{"empty input", "", "", true},
		{"whitespace only", "  ", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeJID(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("NormalizeJID(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("NormalizeJID(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
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
}
