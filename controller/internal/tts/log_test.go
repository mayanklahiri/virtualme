package tts

import (
	"encoding/json"
	"fmt"
	"testing"
)

type memoryList struct {
	values []string
}

func (m *memoryList) LPush(_ string, values ...string) (int64, error) {
	m.values = append(append([]string(nil), values...), m.values...)
	return int64(len(m.values)), nil
}

func (m *memoryList) LTrim(_ string, start, stop int) error {
	if start >= len(m.values) {
		m.values = nil
		return nil
	}
	stop = min(stop, len(m.values)-1)
	m.values = append([]string(nil), m.values[start:stop+1]...)
	return nil
}

func (m *memoryList) LRange(_ string, start, stop int) ([]string, error) {
	if start >= len(m.values) {
		return []string{}, nil
	}
	stop = min(stop, len(m.values)-1)
	return append([]string(nil), m.values[start:stop+1]...), nil
}

func TestSpeechLogRecordsCapsAndBroadcasts(t *testing.T) {
	store := new(memoryList)
	broadcasts := 0
	log := NewLog(store, func([]byte) { broadcasts++ })
	for index := 0; index < 105; index++ {
		err := log.Record(Entry{
			Timestamp: int64(index), Origin: "console", Voice: DefaultVoice,
			Speed: 1, Chars: index, Text: fmt.Sprintf("entry %d", index),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(store.values) != 100 || broadcasts != 105 {
		t.Fatalf("stored=%d broadcasts=%d", len(store.values), broadcasts)
	}
	var message struct {
		Type    string  `json:"type"`
		Entries []Entry `json:"entries"`
	}
	if err := json.Unmarshal(log.Message(), &message); err != nil {
		t.Fatal(err)
	}
	if message.Type != "speech-log" || len(message.Entries) != 50 ||
		message.Entries[0].Text != "entry 104" || message.Entries[49].Text != "entry 55" {
		t.Fatalf("message = %+v", message)
	}
}
