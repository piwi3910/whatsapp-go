package whatsapp

import "testing"

func TestParseInviteCode(t *testing.T) {
	const code = "AbCdEf1234567890"

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"bare code", code, code},
		{"https link", "https://chat.whatsapp.com/" + code, code},
		{"http link", "http://chat.whatsapp.com/" + code, code},
		// These four are the forms the old two-prefix trim got wrong.
		{"invite path segment", "https://chat.whatsapp.com/invite/" + code, code},
		{"query string", "https://chat.whatsapp.com/" + code + "?mode=r_c", code},
		{"fragment", "https://chat.whatsapp.com/" + code + "#top", code},
		{"code query param", "https://chat.whatsapp.com/?code=" + code, code},
		{"trailing slash", "https://chat.whatsapp.com/" + code + "/", code},
		{"scheme-less", "chat.whatsapp.com/" + code, code},
		{"www host", "https://www.chat.whatsapp.com/" + code, code},
		{"uppercase host", "https://CHAT.WHATSAPP.COM/" + code, code},
		{"surrounding whitespace", "  https://chat.whatsapp.com/" + code + "  ", code},
		{"invite path with query", "https://chat.whatsapp.com/invite/" + code + "?x=1", code},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseInviteCode(tt.input)
			if err != nil {
				t.Fatalf("parseInviteCode(%q) error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("parseInviteCode(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseInviteCodeRejects(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"whitespace", "   "},
		{"wrong host", "https://evil.example.com/AbCdEf1234567890"},
		// A lookalike host must not be accepted just because it ends the
		// same way.
		{"host suffix lookalike", "https://chat.whatsapp.com.evil.example/AbCdEf1234"},
		{"no code in path", "https://chat.whatsapp.com/"},
		{"only invite segment", "https://chat.whatsapp.com/invite/"},
		{"code too short", "https://chat.whatsapp.com/abc"},
		{"illegal characters", "https://chat.whatsapp.com/abc def!!"},
		{"bare sentence", "please join my group"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := parseInviteCode(tt.input); err == nil {
				t.Errorf("parseInviteCode(%q) = %q, want error", tt.input, got)
			}
		})
	}
}
