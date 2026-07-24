package metrics

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTierWindowMean(t *testing.T) {
	store := NewStore(t.TempDir())
	base := time.Now().Add(-time.Minute)
	for i := range 15 {
		store.Add(Sample{
			Ts:        base.Add(time.Duration(i) * 2 * time.Second).UnixMilli(),
			Cores:     []float64{float64(i), float64(i * 2)},
			ProcCPU:   []float64{float64(i * 4)},
			ProcMemMB: []int{i, i * 2}, Load1: float64(i),
			MemUsedMB: i * 10, MemTotalMB: 100,
			GPUUtil: float64(i * 3), GPUMemMB: float64(i * 20),
		})
	}
	if len(store.tiers[1].samples) != 1 {
		t.Fatalf("tier 1 samples = %d, want 1", len(store.tiers[1].samples))
	}
	got := store.tiers[1].samples[0]
	if got.Cores[0] != 7 || got.Cores[1] != 14 || got.ProcCPU[0] != 28 || got.ProcMemMB[0] != 7 ||
		got.GPUUtil != 21 || got.GPUMemMB != 140 {
		t.Fatalf("mean = %+v", got)
	}
}

func TestRingCaps(t *testing.T) {
	for i, def := range tierDefs {
		var samples []Sample
		for n := 0; n < def.retention+10; n++ {
			samples = appendCapped(samples, Sample{Ts: int64(n)}, def.retention)
		}
		if len(samples) != def.retention || samples[0].Ts != 10 {
			t.Fatalf("tier %d cap = %d first=%d", i, len(samples), samples[0].Ts)
		}
	}
}

func TestQueryLookback(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Unix(2_000_000, 0)
	store.now = func() time.Time { return now }
	store.tiers[0].samples = []Sample{
		{Ts: now.Add(-16 * time.Minute).UnixMilli()},
		{Ts: now.Add(-14 * time.Minute).UnixMilli()},
		{Ts: now.UnixMilli()},
	}
	res, samples, ok := store.Query("15m")
	if !ok || res != 2 || len(samples) != 2 {
		t.Fatalf("Query = (%d, %d, %v)", res, len(samples), ok)
	}
	if _, _, ok := store.Query("nope"); ok {
		t.Fatal("unknown lookback accepted")
	}
}

func TestLookbackResolutions(t *testing.T) {
	store := NewStore(t.TempDir())
	want := map[string]int{
		"15m": 2,
		"1h":  2,
		"3h":  30,
		"12h": 30,
		"1d":  300,
		"3d":  300,
		"7d":  300,
		"30d": 900,
	}
	for lookback, resolution := range want {
		got, _, ok := store.Query(lookback)
		if !ok || got != resolution {
			t.Errorf("Query(%q) resolution = %d, ok = %v; want %d, true", lookback, got, ok, resolution)
		}
	}
}

func TestPersistLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	store.Add(Sample{
		Ts: time.Now().UnixMilli(), Cores: []float64{25}, ProcMemMB: []int{10},
		GPUUtil: 42.5, GPUMemMB: 1536,
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store.RunPersist(ctx)

	loaded := NewStore(dir)
	loaded.Load()
	if len(loaded.tiers[0].samples) != 1 || loaded.tiers[0].samples[0].Cores[0] != 25 ||
		loaded.tiers[0].samples[0].GPUUtil != 42.5 || loaded.tiers[0].samples[0].GPUMemMB != 1536 {
		t.Fatalf("loaded = %+v", loaded.tiers[0].samples)
	}
}

func TestGPUFieldsOmittedAndOldTierLoads(t *testing.T) {
	payload, err := json.Marshal(Sample{Ts: 1})
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != `{"ts":1,"cores":null,"procCPU":null,"procMemMB":null,"load1":0,"memUsedMB":0,"memTotalMB":0}` {
		t.Fatalf("absent GPU JSON = %s", payload)
	}

	dir := t.TempDir()
	old := `[{"ts":2,"cores":[10],"procMemMB":[20],"load1":0.5,"memUsedMB":30,"memTotalMB":40}]`
	if err := os.WriteFile(filepath.Join(dir, "tier0.json"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	store := NewStore(dir)
	store.Load()
	if len(store.tiers[0].samples) != 1 || store.tiers[0].samples[0].GPUUtil != 0 ||
		store.tiers[0].samples[0].GPUMemMB != 0 {
		t.Fatalf("old tier loaded incorrectly: %+v", store.tiers[0].samples)
	}
}

func TestCorruptTierIsEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tier0.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := NewStore(dir)
	store.Load()
	if len(store.tiers[0].samples) != 0 {
		t.Fatalf("corrupt tier loaded: %+v", store.tiers[0].samples)
	}
}
