package jobs

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mayanklahiri/virtualme/controller/internal/valkey"
)

func TestActivityRecordCapsBroadcastsAndTrims(t *testing.T) {
	server := newMemoryRESP(t)
	frames := make(chan []byte, 1)
	activity := NewActivity(valkey.New(server.addr), func(payload []byte) { frames <- payload })
	activity.now = func() time.Time { return time.UnixMilli(1234) }
	event := ActivityEvent{
		Kind: "tool", Name: "bash", JobID: "job", Summary: strings.Repeat("s", 220),
		Detail: ActivityDetail{
			Args:       map[string]any{"command": strings.Repeat("a", 300)},
			ResultText: strings.Repeat("r", 2100), OK: true,
			ScreenshotThumb: "data:image/jpeg;base64," + strings.Repeat("x", 33*1024),
		},
	}
	if err := activity.Record(event); err != nil {
		t.Fatal(err)
	}
	var frame struct {
		Type  string        `json:"type"`
		Event ActivityEvent `json:"event"`
	}
	if err := json.Unmarshal(<-frames, &frame); err != nil {
		t.Fatal(err)
	}
	command := frame.Event.Detail.Args.(map[string]any)["command"].(string)
	if frame.Type != "activity-event" || frame.Event.ID == "" || frame.Event.TS != 1234 ||
		len([]rune(frame.Event.Summary)) != 200 || len([]rune(command)) != 256 ||
		len([]rune(frame.Event.Detail.ResultText)) != 2048 || frame.Event.Detail.ScreenshotThumb != "" {
		t.Fatalf("sanitized frame = %+v", frame)
	}

	for index := 0; index < activityCap+10; index++ {
		if err := activity.Record(ActivityEvent{Kind: "llm", Name: "generate"}); err != nil {
			t.Fatal(err)
		}
		<-frames
	}
	server.mu.Lock()
	got := len(server.lists[activityKey])
	server.mu.Unlock()
	if got != activityCap {
		t.Fatalf("activity list length = %d, want %d", got, activityCap)
	}
	if events := activity.Events(activityReplayCap); len(events) != activityReplayCap {
		t.Fatalf("replay length = %d", len(events))
	}
}
