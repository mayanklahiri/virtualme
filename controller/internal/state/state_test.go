package state

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mayanklahiri/virtualme/controller/internal/health"
	"github.com/mayanklahiri/virtualme/controller/internal/procstat"
)

func TestReadSystem(t *testing.T) {
	loadavg := "1.25 0.75 0.50 2/100 1234\n"
	meminfo := "MemTotal:       8388608 kB\nMemFree:        100 kB\nMemAvailable:   2097152 kB\n"
	got := ReadSystem(loadavg, meminfo)
	want := System{Load1: 1.25, MemUsedMB: 6144, MemTotalMB: 8192}
	if got != want {
		t.Fatalf("ReadSystem() = %+v, want %+v", got, want)
	}
}

func TestReadSystemMissingData(t *testing.T) {
	if got := ReadSystem("", ""); got != (System{}) {
		t.Fatalf("ReadSystem(empty) = %+v", got)
	}
}

func TestSnapshotJSON(t *testing.T) {
	snapshot := Snapshot{
		Type:      "state",
		Ts:        123,
		UptimeSec: 5,
		OK:        true,
		Services:  []health.Service{{Name: "xvfb", OK: true}},
		System:    System{Load1: 0.5, MemUsedMB: 1, MemTotalMB: 2},
		Processes: []procstat.Proc{{Name: "xvfb", CPUPct: 1.5, MemMB: 42}},
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	for _, field := range []string{
		`"type":"state"`,
		`"ts":123`,
		`"uptimeSec":5`,
		`"ok":true`,
		`"services":[`,
		`"system":{`,
		`"processes":[{"name":"xvfb","cpuPct":1.5,"memMB":42}]`,
	} {
		if !strings.Contains(text, field) {
			t.Errorf("JSON %q missing %q", text, field)
		}
	}
}

func TestRingBufferCapAndOrder(t *testing.T) {
	collector := NewCollector(health.Config{}, t.TempDir(), func([]byte) {})
	for index := range 160 {
		collector.record(fmt.Appendf(nil, `{"ts":%d}`, index))
	}
	payload := collector.HistoryMessage()
	var message struct {
		Type      string `json:"type"`
		Snapshots []struct {
			Ts int `json:"ts"`
		} `json:"snapshots"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatal(err)
	}
	if message.Type != "history" {
		t.Fatalf("type = %q, want history", message.Type)
	}
	if len(message.Snapshots) != 150 {
		t.Fatalf("ring holds %d snapshots, want 150", len(message.Snapshots))
	}
	if message.Snapshots[0].Ts != 10 || message.Snapshots[149].Ts != 159 {
		t.Fatalf("order wrong: first=%d last=%d, want 10..159 oldest-first",
			message.Snapshots[0].Ts, message.Snapshots[149].Ts)
	}
}

func TestHistoryMessageEmpty(t *testing.T) {
	collector := NewCollector(health.Config{}, t.TempDir(), func([]byte) {})
	if got := string(collector.HistoryMessage()); got != `{"type":"history","snapshots":[]}` {
		t.Fatalf("empty history = %s", got)
	}
}
