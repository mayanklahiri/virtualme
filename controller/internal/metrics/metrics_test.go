package metrics

import (
	"context"
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
			ProcMemMB: []int{i, i * 2}, Load1: float64(i),
			MemUsedMB: i * 10, MemTotalMB: 100,
		})
	}
	if len(store.tiers[1].samples) != 1 {
		t.Fatalf("tier 1 samples = %d, want 1", len(store.tiers[1].samples))
	}
	got := store.tiers[1].samples[0]
	if got.Cores[0] != 7 || got.Cores[1] != 14 || got.ProcMemMB[0] != 7 {
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

func TestPersistLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	store.Add(Sample{Ts: time.Now().UnixMilli(), Cores: []float64{25}, ProcMemMB: []int{10}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store.RunPersist(ctx)

	loaded := NewStore(dir)
	loaded.Load()
	if len(loaded.tiers[0].samples) != 1 || loaded.tiers[0].samples[0].Cores[0] != 25 {
		t.Fatalf("loaded = %+v", loaded.tiers[0].samples)
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
