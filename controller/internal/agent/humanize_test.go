package agent

import (
	"context"
	"math/rand"
	"slices"
	"testing"
	"time"
)

type humanizeRunner struct {
	calls [][]string
}

func (r *humanizeRunner) Run(_ context.Context, _ string, args, _ []string, _ string) ([]byte, []byte, error) {
	r.calls = append(r.calls, append([]string(nil), args...))
	return nil, nil, nil
}

func TestHumanDelaySamplesStayBoundedAndFavorLowerValues(t *testing.T) {
	rng := rand.New(rand.NewSource(20260725))
	samples := make([]int, 10_001)
	for index := range samples {
		samples[index] = samplePowerLawMS(rng, toolPauseMinMS, toolPauseMaxMS, humanDelayShape)
		if samples[index] < toolPauseMinMS || samples[index] > toolPauseMaxMS {
			t.Fatalf("sample %d = %d, outside [%d, %d]", index, samples[index], toolPauseMinMS, toolPauseMaxMS)
		}
	}
	slices.Sort(samples)
	if median := samples[len(samples)/2]; median < toolPauseMinMS || median > 5000 {
		t.Fatalf("median tool pause = %dms, want 2000–5000ms", median)
	}
}

func TestHumanTypeUsesOneInvocationPerRuneAndRandomIntervals(t *testing.T) {
	runner := new(humanizeRunner)
	var pauses []time.Duration
	tools := NewLocalTools(Config{
		Runner: runner,
		Sleep: func(_ context.Context, duration time.Duration) bool {
			pauses = append(pauses, duration)
			return true
		},
	})
	tools.rng = rand.New(rand.NewSource(99))

	if _, err := tools.humanType(context.Background(), "aé中"); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 3 {
		t.Fatalf("xdotool calls = %d, want 3: %v", len(runner.calls), runner.calls)
	}
	for index, call := range runner.calls {
		if len(call) != 6 || call[0] != "type" || call[3] != "0" {
			t.Fatalf("call %d = %v", index, call)
		}
	}
	if len(pauses) != 2 {
		t.Fatalf("key pauses = %d, want 2: %v", len(pauses), pauses)
	}
	for index, pause := range pauses {
		if pause < keyPauseMinMS*time.Millisecond || pause > keyPauseMaxMS*time.Millisecond {
			t.Fatalf("key pause %d = %s, outside 10–200ms", index, pause)
		}
	}
}
