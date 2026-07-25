package notifications

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAtomicScriptsContainRequiredOperations(t *testing.T) {
	for name, script := range map[string]string{
		"create": createScript, "mark-one": markOneScript,
		"mark-all": markAllScript, "snapshot": snapshotScript,
	} {
		if strings.TrimSpace(script) == "" || !strings.Contains(script, "redis.call") {
			t.Fatalf("%s script is incomplete", name)
		}
	}
	for _, token := range []string{"HEXISTS", "HGET", "HSET", "LPUSH", "LRANGE", "LTRIM", "HDEL", "ID_CONFLICT"} {
		if !strings.Contains(createScript, token) {
			t.Errorf("create script missing %s", token)
		}
	}
	if !strings.Contains(markOneScript, "HSETNX") || !strings.Contains(markAllScript, "HSETNX") {
		t.Fatal("read scripts must preserve first read timestamp")
	}
}

func TestWireShapesAndSummaries(t *testing.T) {
	n := Notification{
		ID: "00000000000000000000000001", Type: "info", Sender: "agent",
		Title: "Title", Summary: "Summary", OccurredAtMS: 1, CreatedAtMS: 2,
		Detail: Detail{Version: 1, Renderer: "agent", Data: json.RawMessage(`{"x":1}`)},
	}
	summary := summarize(n)
	if summary.ID != n.ID || summary.Renderer != "agent" {
		t.Fatalf("summary = %#v", summary)
	}
	payload, err := stateMessage([]Notification{n}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var frame map[string]any
	if err := json.Unmarshal(payload, &frame); err != nil {
		t.Fatal(err)
	}
	if frame["type"] != "notifications-state" || frame["unread"].(float64) != 1 {
		t.Fatalf("frame = %#v", frame)
	}
}

func TestRequestValidation(t *testing.T) {
	if !validRequestID("a:B_1-2.3") || validRequestID("") || validRequestID(strings.Repeat("x", 65)) {
		t.Fatal("request ID validation mismatch")
	}
	for _, id := range []string{"", "lower000000000000000000000", "0000000000000000000000000I"} {
		if validULID(id) {
			t.Fatalf("accepted invalid id %q", id)
		}
	}
}
