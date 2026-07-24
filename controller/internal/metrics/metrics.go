// Package metrics provides persistent, multi-resolution controller metrics.
package metrics

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Sample is one metrics point. Slice fields use fixed controller-defined order.
type Sample struct {
	Ts         int64     `json:"ts"`
	Cores      []float64 `json:"cores"`
	ProcCPU    []float64 `json:"procCPU"`
	ProcMemMB  []int     `json:"procMemMB"`
	Load1      float64   `json:"load1"`
	MemUsedMB  int       `json:"memUsedMB"`
	MemTotalMB int       `json:"memTotalMB"`
	GPUUtil    float64   `json:"gpuUtil,omitempty"`
	GPUMemMB   float64   `json:"gpuMemMB,omitempty"`
}

type tierDef struct {
	resSec    int
	retention int
	window    int
}

var tierDefs = [...]tierDef{
	{2, 1800, 1},
	{30, 1440, 15},
	{300, 2016, 150},
	{900, 2880, 450},
}

var lookbacks = map[string]struct {
	tier int
	span time.Duration
}{
	"15m": {0, 15 * time.Minute},
	"1h":  {0, time.Hour},
	"3h":  {1, 3 * time.Hour},
	"12h": {1, 12 * time.Hour},
	"1d":  {2, 24 * time.Hour},
	"3d":  {2, 3 * 24 * time.Hour},
	"7d":  {2, 7 * 24 * time.Hour},
	"30d": {3, 30 * 24 * time.Hour},
}

type tier struct {
	samples []Sample
	pending []Sample
}

// Store owns all tiers and their persistence.
type Store struct {
	mu     sync.Mutex
	dir    string
	tiers  [4]tier
	now    func() time.Time
	logged [4]bool
}

// NewStore creates an empty store rooted at dir.
func NewStore(dir string) *Store {
	return &Store{dir: dir, now: time.Now}
}

func cloneSample(sm Sample) Sample {
	sm.Cores = append([]float64(nil), sm.Cores...)
	sm.ProcCPU = append([]float64(nil), sm.ProcCPU...)
	sm.ProcMemMB = append([]int(nil), sm.ProcMemMB...)
	return sm
}

func mean(samples []Sample) Sample {
	result := Sample{Ts: samples[len(samples)-1].Ts}
	coreN, procCPUN, procN := 0, 0, 0
	for _, sm := range samples {
		if len(sm.Cores) > coreN {
			coreN = len(sm.Cores)
		}
		if len(sm.ProcCPU) > procCPUN {
			procCPUN = len(sm.ProcCPU)
		}
		if len(sm.ProcMemMB) > procN {
			procN = len(sm.ProcMemMB)
		}
	}
	result.Cores = make([]float64, coreN)
	result.ProcCPU = make([]float64, procCPUN)
	procSums := make([]int64, procN)
	for _, sm := range samples {
		for i, value := range sm.Cores {
			result.Cores[i] += value
		}
		for i, value := range sm.ProcCPU {
			result.ProcCPU[i] += value
		}
		for i, value := range sm.ProcMemMB {
			procSums[i] += int64(value)
		}
		result.Load1 += sm.Load1
		result.MemUsedMB += sm.MemUsedMB
		result.MemTotalMB += sm.MemTotalMB
		result.GPUUtil += sm.GPUUtil
		result.GPUMemMB += sm.GPUMemMB
	}
	n := float64(len(samples))
	for i := range result.Cores {
		result.Cores[i] /= n
	}
	for i := range result.ProcCPU {
		result.ProcCPU[i] /= n
	}
	result.ProcMemMB = make([]int, procN)
	for i, total := range procSums {
		result.ProcMemMB[i] = int(total / int64(len(samples)))
	}
	result.Load1 /= n
	result.MemUsedMB /= len(samples)
	result.MemTotalMB /= len(samples)
	result.GPUUtil /= n
	result.GPUMemMB /= n
	return result
}

func appendCapped(samples []Sample, sm Sample, cap int) []Sample {
	samples = append(samples, cloneSample(sm))
	if len(samples) > cap {
		samples = append([]Sample(nil), samples[len(samples)-cap:]...)
	}
	return samples
}

// Add appends a raw sample and feeds every aggregate tier.
func (s *Store) Add(sm Sample) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tiers[0].samples = appendCapped(s.tiers[0].samples, sm, tierDefs[0].retention)
	for i := 1; i < len(s.tiers); i++ {
		t := &s.tiers[i]
		t.pending = append(t.pending, cloneSample(sm))
		if len(t.pending) >= tierDefs[i].window {
			t.samples = appendCapped(t.samples, mean(t.pending[:tierDefs[i].window]), tierDefs[i].retention)
			t.pending = t.pending[tierDefs[i].window:]
		}
	}
}

// Query returns the selected tier restricted to the requested trailing span.
func (s *Store) Query(lookback string) (int, []Sample, bool) {
	selection, ok := lookbacks[lookback]
	if !ok {
		return 0, nil, false
	}
	cutoff := s.now().Add(-selection.span).UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	source := s.tiers[selection.tier].samples
	first := 0
	for first < len(source) && source[first].Ts < cutoff {
		first++
	}
	result := make([]Sample, 0, len(source)-first)
	for _, sm := range source[first:] {
		result = append(result, cloneSample(sm))
	}
	return tierDefs[selection.tier].resSec, result, true
}

// Load restores each tier independently. Missing/corrupt files become empty.
func (s *Store) Load() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.tiers {
		path := filepath.Join(s.dir, "tier"+string(rune('0'+i))+".json")
		content, err := os.ReadFile(path)
		var samples []Sample
		if err == nil {
			err = json.Unmarshal(content, &samples)
		}
		if err != nil {
			if !os.IsNotExist(err) && !s.logged[i] {
				log.Printf("metrics: ignoring %s: %v", path, err)
				s.logged[i] = true
			}
			s.tiers[i].samples = nil
			continue
		}
		if len(samples) > tierDefs[i].retention {
			samples = samples[len(samples)-tierDefs[i].retention:]
		}
		s.tiers[i].samples = samples
	}
}

func (s *Store) persist() {
	s.mu.Lock()
	copies := make([][]Sample, len(s.tiers))
	for i := range s.tiers {
		copies[i] = append([]Sample(nil), s.tiers[i].samples...)
	}
	s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		log.Println("metrics: create directory:", err)
		return
	}
	for i, samples := range copies {
		payload, err := json.Marshal(samples)
		if err != nil {
			continue
		}
		path := filepath.Join(s.dir, "tier"+string(rune('0'+i))+".json")
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, payload, 0o644); err != nil {
			log.Println("metrics: persist:", err)
			continue
		}
		if err := os.Rename(tmp, path); err != nil {
			log.Println("metrics: rename:", err)
		}
	}
}

// RunPersist writes all tiers every minute and once more on cancellation.
func (s *Store) RunPersist(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.persist()
		case <-ctx.Done():
			s.persist()
			return
		}
	}
}
