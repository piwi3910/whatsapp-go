package whatsapp

import (
	"strings"
	"testing"
)

func TestVCardEscape(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain", "31612345678", "31612345678"},
		{"backslash", `a\b`, `a\\b`},
		{"comma", "a,b", `a\,b`},
		{"semicolon", "a;b", `a\;b`},
		{"newline", "a\nb", `a\nb`},
		{"carriage return dropped", "a\r\nb", `a\nb`},
		{"unicode preserved", "Zoë 🙂", "Zoë 🙂"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := vcardEscape(tt.input); got != tt.want {
				t.Errorf("vcardEscape(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// The injection this locks: an unescaped newline in the value used to end the
// TEL property, letting the caller append arbitrary vCard properties (e.g. a
// second TEL, or an EMAIL) to the card the recipient receives.
func TestBuildVCardResistsInjection(t *testing.T) {
	hostile := "31612345678\r\nTEL:31600000000\r\nEMAIL:attacker@example.com"
	card := buildVCard(hostile, hostile)

	lines := strings.Split(card, "\r\n")
	want := []string{"BEGIN:VCARD", "VERSION:3.0"}
	for i, w := range want {
		if lines[i] != w {
			t.Fatalf("line %d = %q, want %q", i, lines[i], w)
		}
	}
	if len(lines) != 5 {
		t.Fatalf("vCard has %d lines, want 5 (injection created extra properties):\n%s", len(lines), card)
	}
	if lines[len(lines)-1] != "END:VCARD" {
		t.Errorf("last line = %q, want END:VCARD", lines[len(lines)-1])
	}
	// The injected text must survive only as escaped content inside a value,
	// never as a property of its own — so count property lines, not
	// substrings.
	var telLines, emailLines int
	for _, line := range lines {
		if strings.HasPrefix(line, "TEL") {
			telLines++
		}
		if strings.HasPrefix(line, "EMAIL") {
			emailLines++
		}
	}
	if telLines != 1 {
		t.Errorf("expected exactly one TEL property, got %d:\n%s", telLines, card)
	}
	if emailLines != 0 {
		t.Errorf("injected EMAIL property survived escaping:\n%s", card)
	}
}

func TestBuildVCardWellFormed(t *testing.T) {
	card := buildVCard("31612345678", "31612345678")
	for _, want := range []string{
		"BEGIN:VCARD\r\n",
		"VERSION:3.0\r\n",
		"FN:31612345678\r\n",
		"TEL;type=CELL;waid=31612345678:+31612345678\r\n",
		"END:VCARD",
	} {
		if !strings.Contains(card, want) {
			t.Errorf("vCard missing %q:\n%s", want, card)
		}
	}
}

func TestDetectMIME(t *testing.T) {
	tests := []struct {
		filename string
		data     []byte
		want     string
	}{
		{"photo.jpg", nil, "image/jpeg"},
		{"photo.PNG", []byte{0x89, 'P', 'N', 'G'}, "image/png"}, // extension match is case-insensitive
		{"clip.mp4", nil, "video/mp4"},
		{"doc.pdf", nil, "application/pdf"},
		{"noext", nil, "application/octet-stream"},
	}
	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			if got := detectMIME(tt.filename, tt.data); got != tt.want {
				t.Errorf("detectMIME(%q) = %q, want %q", tt.filename, got, tt.want)
			}
		})
	}
}

func TestParseJIDRejectsGarbage(t *testing.T) {
	c := &Client{}
	for _, input := range []string{
		"",
		"not a jid",
		"1234567890@evil.example.com",
		"abc@s.whatsapp.net",
	} {
		if _, err := c.parseJID(input); err == nil {
			t.Errorf("parseJID(%q) = nil error, want rejection", input)
		}
	}
	if got, err := c.parseJID("+31612345678"); err != nil || got.String() != "31612345678@s.whatsapp.net" {
		t.Errorf("parseJID(+31612345678) = (%v, %v)", got, err)
	}
}
