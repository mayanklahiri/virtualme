package agent

import (
	"strings"
	"testing"
)

func TestEncodeYAMLOrderAndQuoting(t *testing.T) {
	value := map[string]any{
		"body": []any{map[string]any{
			"sel":  `body > div:nth-of-type(1)`,
			"tag":  "h1",
			"text": "Hello \"world\"\nline two",
		}},
		"title": "Example",
		"url":   "https://example.com/",
		"head": map[string]any{
			"canonical": "https://example.com/",
			"lang":      "en",
		},
	}
	got := encodeYAML(value)
	wantParts := []string{
		`title: "Example"`,
		`url: "https://example.com/"`,
		"head:",
		`lang: "en"`,
		"body:",
		`- tag: "h1"`,
		`text: "Hello \"world\"\nline two"`,
	}
	for _, part := range wantParts {
		if !strings.Contains(got, part) {
			t.Fatalf("missing %q in:\n%s", part, got)
		}
	}
	if strings.Index(got, "title:") > strings.Index(got, "url:") {
		t.Fatalf("title must precede url:\n%s", got)
	}
}

func TestDigestToYAMLBudgetMarker(t *testing.T) {
	body := make([]any, 0, 200)
	for range 200 {
		body = append(body, map[string]any{
			"tag":  "p",
			"sel":  "body > p:nth-of-type(1)",
			"text": strings.Repeat("x", 120),
		})
	}
	digest := map[string]any{
		"title": "Big",
		"url":   "https://example.com/",
		"head":  map[string]any{},
		"body":  body,
	}
	got := digestToYAML(digest)
	if len(got) > readPageCap {
		t.Fatalf("digest length %d exceeds cap %d", len(got), readPageCap)
	}
	if !strings.Contains(got, "truncated: page digest exceeded budget") {
		t.Fatalf("expected budget marker in truncated digest")
	}
}

func TestReadPageExpressionUsesEmbeddedScript(t *testing.T) {
	expr := readPageExpression()
	if !strings.Contains(expr, "NODE_BUDGET") {
		t.Fatalf("embedded read page script missing: %q", expr[:min(80, len(expr))])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
