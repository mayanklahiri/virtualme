package state

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mayanklahiri/virtualme/controller/internal/gpu"
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

func TestReadDisk(t *testing.T) {
	freeMB, totalMB := ReadDisk(t.TempDir())
	if freeMB <= 0 || totalMB <= 0 || freeMB > totalMB {
		t.Fatalf("ReadDisk(tempdir) = %d, %d", freeMB, totalMB)
	}
}

func TestReadDiskMissingPath(t *testing.T) {
	if freeMB, totalMB := ReadDisk(t.TempDir() + "/missing"); freeMB != 0 || totalMB != 0 {
		t.Fatalf("ReadDisk(missing) = %d, %d", freeMB, totalMB)
	}
}

func TestSnapshotJSON(t *testing.T) {
	snapshot := Snapshot{
		Type:      "state",
		Ts:        123,
		UptimeSec: 5,
		Hostname:  "virtualme",
		OK:        true,
		Services:  []health.Service{{Name: "xvfb", OK: true}},
		System:    System{Load1: 0.5, MemUsedMB: 1, MemTotalMB: 2, DiskFreeMB: 3, DiskTotalMB: 4, GPUUtil: 25, GPUMemMB: 512, GPUMemTotalMB: 1024},
		Processes: []procstat.Proc{{Name: "xvfb", CPUPct: 1.5, MemMB: 42}},
		Cores:     []float64{25},
		Scheduler: Scheduler{LocalTime: "2026-07-22T09:00:00-07:00", TZ: "America/Los_Angeles", Active: []string{"morning", "anytime"}},
		Jiggler:   Jiggler{Enabled: true},
		GPU:       gpu.Info{Present: true, Vendor: "nvidia", Model: "Test GPU", Params: []gpu.KV{{Key: "VRAM", Value: "1024 MiB"}}, Sampler: "nvidia-smi"},
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
		`"hostname":"virtualme"`,
		`"ok":true`,
		`"services":[`,
		`"system":{`,
		`"gpuUtil":25`,
		`"gpuMemMB":512`,
		`"gpuMemTotalMB":1024`,
		`"processes":[{"name":"xvfb","cpuPct":1.5,"memMB":42}]`,
		`"cores":[25]`,
		`"scheduler":{"localTime":"2026-07-22T09:00:00-07:00","tz":"America/Los_Angeles","active":["morning","anytime"]}`,
		`"jiggler":{"enabled":true}`,
		`"gpu":{"present":true,"vendor":"nvidia","model":"Test GPU","params":[{"key":"VRAM","value":"1024 MiB"}],"sampler":"nvidia-smi"}`,
	} {
		if !strings.Contains(text, field) {
			t.Errorf("JSON %q missing %q", text, field)
		}
	}
}

func TestSchedulerStateUsesLocalClock(t *testing.T) {
	location, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TZ", "America/Los_Angeles")
	got := schedulerState(time.Date(2026, 7, 22, 9, 0, 0, 0, location))
	if got.TZ != "America/Los_Angeles" || got.LocalTime != "2026-07-22T09:00:00-07:00" ||
		len(got.Active) == 0 || got.Active[0] != "morning" {
		t.Fatalf("schedulerState = %+v", got)
	}
}
