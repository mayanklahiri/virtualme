package tts

import (
	"encoding/json"
)

const speechLogKey = "virtualme:speech:log"

// Entry is one completed speech synthesis retained in the global history.
type Entry struct {
	Timestamp  int64   `json:"ts"`
	Origin     string  `json:"origin"`
	Voice      string  `json:"voice"`
	Speed      float64 `json:"speed"`
	Chars      int     `json:"chars"`
	DurationMS int64   `json:"durationMs"`
	Cached     bool    `json:"cached"`
	Text       string  `json:"text"`
}

type listStore interface {
	LPush(string, ...string) (int64, error)
	LTrim(string, int, int) error
	LRange(string, int, int) ([]string, error)
}

// Log persists speech history and broadcasts snapshots after successful writes.
type Log struct {
	store     listStore
	broadcast func([]byte)
}

// NewLog creates a speech history backed by the shared Valkey client.
func NewLog(store listStore, broadcast func([]byte)) *Log {
	return &Log{store: store, broadcast: broadcast}
}

// Record prepends and caps one completed synthesis.
func (l *Log) Record(entry Entry) error {
	encoded, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	if _, err := l.store.LPush(speechLogKey, string(encoded)); err != nil {
		return err
	}
	if err := l.store.LTrim(speechLogKey, 0, 99); err != nil {
		return err
	}
	if l.broadcast != nil {
		l.broadcast(l.Message())
	}
	return nil
}

// Message returns the newest 50 valid entries as a websocket frame.
func (l *Log) Message() []byte {
	values, err := l.store.LRange(speechLogKey, 0, 49)
	entries := make([]Entry, 0, len(values))
	if err == nil {
		for _, value := range values {
			var entry Entry
			if json.Unmarshal([]byte(value), &entry) == nil {
				entries = append(entries, entry)
			}
		}
	}
	payload, _ := json.Marshal(struct {
		Type    string  `json:"type"`
		Entries []Entry `json:"entries"`
	}{Type: "speech-log", Entries: entries})
	return payload
}

// HandleMessage responds to a speech history request on one connection.
func (l *Log) HandleMessage(payload []byte, send func([]byte) error) bool {
	var request struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(payload, &request) != nil || request.Type != "speech-log-req" {
		return false
	}
	_ = send(l.Message())
	return true
}
