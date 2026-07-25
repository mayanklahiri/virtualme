package agent

import (
	"context"
	"math"
	"math/rand"
	"time"
)

const (
	humanDelayShape = 2.5

	toolPauseMinMS = 2000
	toolPauseMaxMS = 7000
	stepPauseMinMS = 150
	stepPauseMaxMS = 800
	keyPauseMinMS  = 10
	keyPauseMaxMS  = 200
	scrollMinMS    = 80
	scrollMaxMS    = 350
)

func samplePowerLawMS(rng *rand.Rand, minMS, maxMS int, shape float64) int {
	if maxMS <= minMS {
		return minMS
	}
	return minMS + int(math.Round(float64(maxMS-minMS)*math.Pow(rng.Float64(), shape)))
}

func sampleToolPause(rng *rand.Rand) time.Duration {
	return time.Duration(samplePowerLawMS(rng, toolPauseMinMS, toolPauseMaxMS, humanDelayShape)) * time.Millisecond
}

func sampleStepPause(rng *rand.Rand) time.Duration {
	return time.Duration(samplePowerLawMS(rng, stepPauseMinMS, stepPauseMaxMS, humanDelayShape)) * time.Millisecond
}

func sampleKeyPause(rng *rand.Rand) time.Duration {
	return time.Duration(samplePowerLawMS(rng, keyPauseMinMS, keyPauseMaxMS, humanDelayShape)) * time.Millisecond
}

func sampleScrollTickPause(rng *rand.Rand) time.Duration {
	return time.Duration(samplePowerLawMS(rng, scrollMinMS, scrollMaxMS, humanDelayShape)) * time.Millisecond
}

func sleepContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (t *localTools) pause(ctx context.Context, duration time.Duration) error {
	if !t.humanize || t.sleep(ctx, duration) {
		return nil
	}
	return ctx.Err()
}

func (t *localTools) pauseTool(ctx context.Context) error {
	return t.pause(ctx, sampleToolPause(t.rng))
}

func (t *localTools) pauseStep(ctx context.Context) error {
	return t.pause(ctx, sampleStepPause(t.rng))
}

func (t *localTools) humanType(ctx context.Context, text string) (ToolResult, error) {
	characters := []rune(text)
	for index, character := range characters {
		if _, err := t.action(ctx, "", "type", "--clearmodifiers", "--delay", "0", "--", string(character)); err != nil {
			return ToolResult{}, err
		}
		if index < len(characters)-1 {
			if err := t.pause(ctx, sampleKeyPause(t.rng)); err != nil {
				return ToolResult{}, err
			}
		}
	}
	return ToolResult{}, nil
}

func (t *localTools) humanScroll(ctx context.Context, dir string, amount int) (ToolResult, error) {
	button := "5"
	if dir == "up" {
		button = "4"
	}
	for index := range amount {
		if _, err := t.action(ctx, "", "click", button); err != nil {
			return ToolResult{}, err
		}
		if index < amount-1 {
			if err := t.pause(ctx, sampleScrollTickPause(t.rng)); err != nil {
				return ToolResult{}, err
			}
		}
	}
	return ToolResult{Text: "Scrolled " + dir, Summary: "Scrolled " + dir}, nil
}
