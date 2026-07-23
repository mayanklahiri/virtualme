package procstat

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseStat(t *testing.T) {
	cases := []struct {
		name    string
		content string
		ticks   uint64
		ok      bool
	}{
		{
			name:    "plain comm",
			content: "42 (llama-server) S 1 42 42 0 -1 4194304 100 0 0 0 250 50 0 0 20 0 8 0 12345 0 0",
			ticks:   300,
			ok:      true,
		},
		{
			name:    "comm with parens and spaces",
			content: "7 (comm with) parens) R 1 7 7 0 -1 0 0 0 0 0 10 5 0 0 20 0 1 0 100 0 0",
			ticks:   15,
			ok:      true,
		},
		{
			name:    "truncated",
			content: "42 (x) S 1 2",
			ok:      false,
		},
		{
			name:    "no paren",
			content: "garbage",
			ok:      false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ticks, ok := parseStat(tc.content)
			if ok != tc.ok || ticks != tc.ticks {
				t.Fatalf("parseStat = (%d, %v), want (%d, %v)", ticks, ok, tc.ticks, tc.ok)
			}
		})
	}
}

func TestParseStatm(t *testing.T) {
	if pages, ok := parseStatm("1000 256 100 10 0 500 0"); !ok || pages != 256 {
		t.Fatalf("parseStatm = (%d, %v), want (256, true)", pages, ok)
	}
	if _, ok := parseStatm("1000"); ok {
		t.Fatal("parseStatm accepted truncated content")
	}
}

func writeProc(t *testing.T, root string, pid int, comm string, ticks, rssPages uint64) {
	t.Helper()
	dir := filepath.Join(root, fmt.Sprint(pid))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stat := fmt.Sprintf("%d (%s) S 1 %d %d 0 -1 0 0 0 0 0 %d 0 0 0 20 0 1 0 100 0 0",
		pid, comm, pid, pid, ticks)
	if err := os.WriteFile(filepath.Join(dir, "comm"), []byte(comm+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o644); err != nil {
		t.Fatal(err)
	}
	statm := fmt.Sprintf("%d %d 0 0 0 0 0", rssPages*2, rssPages)
	if err := os.WriteFile(filepath.Join(dir, "statm"), []byte(statm), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSampleAggregationAndOrder(t *testing.T) {
	root := t.TempDir()
	pageSize := os.Getpagesize()
	mb := uint64(1024 * 1024)
	pagesPerHundredMB := 100 * mb / uint64(pageSize)

	writeProc(t, root, 10, "Xvfb", 100, pagesPerHundredMB)
	writeProc(t, root, 20, "chromium", 200, pagesPerHundredMB)
	writeProc(t, root, 21, "chromium", 300, pagesPerHundredMB)
	writeProc(t, root, 99, "unrelated", 999, pagesPerHundredMB)

	sampler := NewSampler(root)
	base := time.Unix(1000, 0)

	first := sampler.sampleAt(base)
	wantOrder := []string{"xvfb", "openbox", "x11vnc", "novnc", "valkey", "llama", "chromium", "controller"}
	if len(first) != len(wantOrder) {
		t.Fatalf("got %d entries, want %d", len(first), len(wantOrder))
	}
	for index, name := range wantOrder {
		if first[index].Name != name {
			t.Fatalf("entry %d name = %q, want %q", index, first[index].Name, name)
		}
		if first[index].CPUPct != 0 {
			t.Fatalf("first sample %s CPU%% = %v, want 0", name, first[index].CPUPct)
		}
	}
	if first[6].MemMB != 200 {
		t.Fatalf("chromium MemMB = %d, want 200 (two aggregated pids)", first[6].MemMB)
	}
	if first[0].MemMB != 100 {
		t.Fatalf("xvfb MemMB = %d, want 100", first[0].MemMB)
	}
	if first[4].MemMB != 0 || first[4].CPUPct != 0 {
		t.Fatalf("valkey (absent) = %+v, want zeros", first[4])
	}

	// One second later: Xvfb +50 ticks (50% at 100 Hz), chromium pids +30 and +20 (50% combined).
	writeProc(t, root, 10, "Xvfb", 150, pagesPerHundredMB)
	writeProc(t, root, 20, "chromium", 230, pagesPerHundredMB)
	writeProc(t, root, 21, "chromium", 320, pagesPerHundredMB)

	second := sampler.sampleAt(base.Add(time.Second))
	if diff := second[0].CPUPct - 50; diff > 0.01 || diff < -0.01 {
		t.Fatalf("xvfb CPU%% = %v, want 50", second[0].CPUPct)
	}
	if diff := second[6].CPUPct - 50; diff > 0.01 || diff < -0.01 {
		t.Fatalf("chromium CPU%% = %v, want 50", second[6].CPUPct)
	}
}

func TestSampleMissingProcRoot(t *testing.T) {
	sampler := NewSampler(filepath.Join(t.TempDir(), "nope"))
	result := sampler.Sample()
	if len(result) != 8 {
		t.Fatalf("got %d entries, want 8", len(result))
	}
	for _, proc := range result {
		if proc.CPUPct != 0 || proc.MemMB != 0 {
			t.Fatalf("%s = %+v, want zeros", proc.Name, proc)
		}
	}
}
