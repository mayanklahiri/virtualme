package telegram

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAuthorizationIsChatAndOptionalUser(t *testing.T) {
	cfg := Config{AllowedChatIDs: []string{"-100", "42"}, AllowedUserIDs: []string{"7"}}
	cases := []struct {
		chat, user string
		bot, want  bool
	}{
		{"-100", "7", false, true},
		{"42", "7", false, true},
		{"99", "7", false, false},
		{"-100", "8", false, false},
		{"-100", "7", true, false},
	}
	for _, tc := range cases {
		if got := Authorized(cfg, tc.chat, tc.user, tc.bot); got != tc.want {
			t.Errorf("Authorized(%q,%q,%v)=%v want %v", tc.chat, tc.user, tc.bot, got, tc.want)
		}
	}
	cfg.AllowedUserIDs = nil
	if !Authorized(cfg, "-100", "999", false) {
		t.Fatal("empty user allowlist must admit humans in allowed chat")
	}
}

func TestEventRedactionAndRawRetention(t *testing.T) {
	raw := json.RawMessage(`{"update_id":12,"message":{"text":"denied secret text"}}`)
	event := NewEvent(Update{UpdateID: 12, Raw: raw}, "denied", "FAKE_TOKEN")
	if event.TextPreview != "" || event.RawOmitted {
		t.Fatalf("unexpected denied event: %+v", event)
	}
	encoded, _ := json.Marshal(event)
	if strings.Contains(string(encoded), "FAKE_TOKEN") {
		t.Fatal("active token leaked")
	}
	oversized := json.RawMessage(`{"x":"` + strings.Repeat("a", 17000) + `"}`)
	event = NewEvent(Update{UpdateID: 13, Raw: oversized}, "accepted", "FAKE_TOKEN")
	if !event.RawOmitted || string(event.RawUpdate) != "{}" {
		t.Fatalf("oversized raw was retained: %+v", event)
	}
}
