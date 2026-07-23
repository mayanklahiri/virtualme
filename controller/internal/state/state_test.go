package state

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mayanklahiri/virtualme/controller/internal/health"
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
	} {
		if !strings.Contains(text, field) {
			t.Errorf("JSON %q missing %q", text, field)
		}
	}
}
