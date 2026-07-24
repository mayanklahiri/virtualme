package prompts

import (
	"strings"
	"testing"
)

func TestPromptsEmbedded(t *testing.T) {
	if strings.TrimSpace(Agent) == "" || strings.TrimSpace(Chat) == "" {
		t.Fatal("embedded prompts must be non-empty")
	}
	for _, placeholder := range []string{
		"{{API_W}}", "{{API_H}}", "{{DISPLAY_W}}", "{{DISPLAY_H}}", "{{MANIFEST}}",
	} {
		if count := strings.Count(Agent, placeholder); count != 1 {
			t.Errorf("agent prompt contains %s %d times, want exactly once", placeholder, count)
		}
	}
	if strings.Contains(Chat, "{{") {
		t.Fatalf("chat prompt contains a placeholder: %q", Chat)
	}
}
