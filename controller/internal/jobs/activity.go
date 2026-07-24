package jobs

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/mayanklahiri/virtualme/controller/internal/valkey"
	"github.com/mayanklahiri/virtualme/controller/internal/ws"
)

const (
	activityKey       = "virtualme:activity"
	activityCap       = 500
	activityReplayCap = 100
)

// ActivityDetail contains bounded, type-specific event data.
type ActivityDetail struct {
	Args             any    `json:"args,omitempty"`
	ResultText       string `json:"resultText,omitempty"`
	DurationMS       int64  `json:"durationMs,omitempty"`
	OK               bool   `json:"ok"`
	PromptTokens     int    `json:"promptTokens,omitempty"`
	CompletionTokens int    `json:"completionTokens,omitempty"`
	ScreenshotThumb  string `json:"screenshotThumb,omitempty"`
	Phase            string `json:"phase,omitempty"`
	Stopped          bool   `json:"stopped,omitempty"`
	Chars            int    `json:"chars,omitempty"`
	Voice            string `json:"voice,omitempty"`
	RecipientDomain  string `json:"recipientDomain,omitempty"`
	Size             int    `json:"size,omitempty"`
}

// ActivityEvent is one durable externally-visible machine action.
type ActivityEvent struct {
	ID      string         `json:"id"`
	TS      int64          `json:"ts"`
	Kind    string         `json:"kind"`
	Name    string         `json:"name"`
	JobID   string         `json:"jobId"`
	Summary string         `json:"summary"`
	Detail  ActivityDetail `json:"detail"`
}

// ActivityRecorder is implemented by the durable ledger and test recorders.
type ActivityRecorder interface {
	Record(ActivityEvent) error
}

// Activity stores and broadcasts bounded activity events.
type Activity struct {
	client    *valkey.Client
	broadcast func([]byte)
	now       func() time.Time
}

type jobIDContextKey struct{}

func withJobID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, jobIDContextKey{}, id)
}

// JobID returns the current queue envelope ID, when execution is queued.
func JobID(ctx context.Context) string {
	id, _ := ctx.Value(jobIDContextKey{}).(string)
	return id
}

// NewActivity constructs an activity ledger.
func NewActivity(client *valkey.Client, broadcast func([]byte)) *Activity {
	return &Activity{client: client, broadcast: broadcast, now: time.Now}
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func truncateActivityValue(value any) any {
	switch typed := value.(type) {
	case string:
		return truncateRunes(typed, 256)
	case []any:
		for index, item := range typed {
			typed[index] = truncateActivityValue(item)
		}
	case map[string]any:
		for key, item := range typed {
			typed[key] = truncateActivityValue(item)
		}
	}
	return value
}

func sanitizeActivity(event *ActivityEvent) {
	if event.ID == "" {
		event.ID = NewID()
	}
	event.Summary = truncateRunes(strings.TrimSpace(event.Summary), 200)
	event.Detail.ResultText = truncateRunes(event.Detail.ResultText, 2048)
	event.Detail.Args = truncateActivityValue(event.Detail.Args)
	if len(event.Detail.ScreenshotThumb) > 32*1024 ||
		(event.Detail.ScreenshotThumb != "" && !strings.HasPrefix(event.Detail.ScreenshotThumb, "data:image/")) {
		event.Detail.ScreenshotThumb = ""
	}
}

// Record persists and broadcasts one sanitized event.
func (a *Activity) Record(event ActivityEvent) error {
	if event.TS == 0 {
		event.TS = a.now().UnixMilli()
	}
	sanitizeActivity(&event)
	encoded, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err := a.client.LPush(activityKey, string(encoded)); err != nil {
		return err
	}
	if err := a.client.LTrim(activityKey, 0, activityCap-1); err != nil {
		return err
	}
	if a.broadcast != nil {
		frame, _ := json.Marshal(map[string]any{"type": "activity-event", "event": event})
		a.broadcast(frame)
	}
	return nil
}

// Events returns newest-first activity events.
func (a *Activity) Events(limit int) []ActivityEvent {
	if limit <= 0 || limit > activityCap {
		limit = activityReplayCap
	}
	items, err := a.client.LRange(activityKey, 0, limit-1)
	if err != nil {
		log.Println("activity: replay failed:", err)
		return []ActivityEvent{}
	}
	events := make([]ActivityEvent, 0, len(items))
	for _, item := range items {
		var event ActivityEvent
		if json.Unmarshal([]byte(item), &event) == nil {
			events = append(events, event)
		}
	}
	return events
}

// Message returns the websocket activity replay frame.
func (a *Activity) Message() []byte {
	payload, _ := json.Marshal(map[string]any{"type": "activity", "events": a.Events(activityReplayCap)})
	return payload
}

// HandleMessage handles an activity-req websocket frame.
func (a *Activity) HandleMessage(conn *ws.Conn, payload []byte) bool {
	var request struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(payload, &request) != nil || request.Type != "activity-req" {
		return false
	}
	_ = conn.WriteText(a.Message())
	return true
}
