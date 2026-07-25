package metrics

import "sync"

// CounterSnapshot is one drained batch of LLM and action counters.
type CounterSnapshot struct {
	TokIn        int     `json:"tokIn"`
	TokOut       int     `json:"tokOut"`
	TokCached    int     `json:"tokCached"`
	LLMPromptMs  float64 `json:"llmPromptMs"`
	LLMPredictMs float64 `json:"llmPredictMs"`
	ActObserve   int     `json:"actObserve"`
	ActActuate   int     `json:"actActuate"`
	ActBash      int     `json:"actBash"`
	ActSpeak     int     `json:"actSpeak"`
}

// Counters accumulates LLM usage and agent-action deltas between samples.
type Counters struct {
	mu      sync.Mutex
	pending CounterSnapshot
}

// AddLLM records one finished LLM call's token counts and phase timings.
func (c *Counters) AddLLM(promptTok, completionTok, cachedTok int, promptMs, predictMs float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pending.TokIn += promptTok
	c.pending.TokOut += completionTok
	c.pending.TokCached += cachedTok
	c.pending.LLMPromptMs += promptMs
	c.pending.LLMPredictMs += predictMs
}

// AddAction records one executed agent action by category.
func (c *Counters) AddAction(category string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch category {
	case "observe":
		c.pending.ActObserve++
	case "bash":
		c.pending.ActBash++
	case "speak":
		c.pending.ActSpeak++
	default:
		c.pending.ActActuate++
	}
}

// Drain returns the accumulated deltas and resets them to zero.
func (c *Counters) Drain() CounterSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	snapshot := c.pending
	c.pending = CounterSnapshot{}
	return snapshot
}
