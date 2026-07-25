// Package agent implements Virtual Me's local, OS-level browser-control loop.
package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mayanklahiri/virtualme/controller/internal/jobs"
	"github.com/mayanklahiri/virtualme/controller/internal/metrics"
	"github.com/mayanklahiri/virtualme/controller/internal/tts"
	"github.com/mayanklahiri/virtualme/controller/prompts"
)

const (
	defaultMaxSteps      = 500
	defaultKeep          = 20
	defaultContextTokens = 32768
	maxToolRounds        = 4
	toolTextCap          = 8 * 1024
	storedObservationCap = 64 * 1024
)

var errContextExceeded = errors.New("model context exceeds the configured limit")

// Tool is an OpenAI-compatible function tool.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"parameters"`
}

// PromptMessage is an OpenAI-compatible conversation message.
type PromptMessage struct {
	Role       string     `json:"role"`
	Content    any        `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ToolCall is a model-requested function call.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall holds a function name and JSON arguments.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Config configures an Agent. Command paths are injectable for hermetic tests.
type Config struct {
	LlamaURL      string
	CDPURL        string
	Display       string
	Resolution    string
	XdotoolPath   string
	ScrotPath     string
	ConvertPath   string
	BashPath      string
	DataDir       string
	Manifest      string
	MaxSteps      int
	KeepTasks     int
	ContextTokens int
	Broadcast     func([]byte)
	History       func() []PromptMessage
	Client        *http.Client
	Runner        Runner
	Executor      Executor
	Humanize      *bool
	Sleep         func(context.Context, time.Duration) bool
	TTS           *tts.Client
	Activity      jobs.ActivityRecorder
	Counters      *metrics.Counters
}

// ActionCategory maps one executed tool to a metrics action category.
func ActionCategory(toolName string, observe bool) string {
	switch {
	case toolName == "bash":
		return "bash"
	case toolName == "speak":
		return "speak"
	case observe:
		return "observe"
	default:
		return "actuate"
	}
}

// Result describes how an agent task terminated.
type Result struct {
	Reply            string
	Stopped          bool
	Failed           bool
	PromptTokens     int
	CompletionTokens int
}

// Executor runs one model tool.
type Executor interface {
	Definitions() []Tool
	Execute(context.Context, string, json.RawMessage) (ToolResult, error)
}

// ToolResult is returned to the model after a tool executes.
type ToolResult struct {
	Text      string
	ImageJPEG []byte
	Summary   string
	Observe   bool
}

// Agent owns the model/tool loop.
type Agent struct {
	cfg     Config
	tools   Executor
	system  string
	mu      sync.Mutex
	taskID  string
	taskDir string
	step    int

	bytesPerToken float64

	replayMu sync.Mutex
	replay   [][]byte
}

const replayCap = 200

// ReplayFrames returns the buffered agent-step frames for the most recent
// task, so reconnecting clients can restore the step cards that a
// chat-history re-render wiped.
func (a *Agent) ReplayFrames() [][]byte {
	a.replayMu.Lock()
	defer a.replayMu.Unlock()
	frames := make([][]byte, len(a.replay))
	copy(frames, a.replay)
	return frames
}

func (a *Agent) resetReplay() {
	a.replayMu.Lock()
	a.replay = nil
	a.replayMu.Unlock()
}

func (a *Agent) bufferReplay(frame []byte) {
	a.replayMu.Lock()
	a.replay = append(a.replay, frame)
	if len(a.replay) > replayCap {
		a.replay = a.replay[len(a.replay)-replayCap:]
	}
	a.replayMu.Unlock()
}

// New constructs an agent and its default local tool executor.
func New(cfg Config) *Agent {
	if cfg.MaxSteps <= 0 {
		cfg.MaxSteps = defaultMaxSteps
	}
	if cfg.KeepTasks <= 0 {
		cfg.KeepTasks = defaultKeep
	}
	if cfg.ContextTokens <= 0 {
		cfg.ContextTokens = defaultContextTokens
	}
	if cfg.Broadcast == nil {
		cfg.Broadcast = func([]byte) {}
	}
	if cfg.Client == nil {
		// Large CPU-only prompts can legitimately take longer than five
		// minutes at the 32K context. The request context remains cancellable.
		cfg.Client = &http.Client{}
	}
	if cfg.DataDir == "" {
		cfg.DataDir = filepath.Join(os.TempDir(), "virtualme-agent")
	}
	if cfg.Manifest == "" {
		cfg.Manifest = "/opt/agent/system-manifest.json"
	}
	a := &Agent{cfg: cfg, bytesPerToken: defaultBytesPerToken}
	if cfg.Executor != nil {
		a.tools = cfg.Executor
	} else {
		a.tools = NewLocalTools(cfg)
	}
	a.system = buildSystemPrompt(cfg)
	return a
}

func buildSystemPrompt(cfg Config) string {
	manifest, _ := os.ReadFile(cfg.Manifest)
	environment := strings.TrimSpace(string(manifest))
	if len(environment) > 4096 {
		environment = environment[:4096]
	}
	width, height := parseResolution(cfg.Resolution)
	apiWidth, apiHeight := apiDimensions(width, height)
	return strings.NewReplacer(
		"{{API_W}}", strconv.Itoa(apiWidth),
		"{{API_H}}", strconv.Itoa(apiHeight),
		"{{DISPLAY_W}}", strconv.Itoa(width),
		"{{DISPLAY_H}}", strconv.Itoa(height),
		"{{MANIFEST}}", environment,
	).Replace(prompts.Agent)
}

func (a *Agent) broadcast(value any) {
	payload, _ := json.Marshal(value)
	a.cfg.Broadcast(payload)
}

func (a *Agent) status(phase string) {
	a.broadcast(map[string]any{
		"type": "agent-status", "taskId": a.taskID, "phase": phase, "n": a.step,
	})
}

// Handle executes one user task with the configured conversation history.
func (a *Agent) Handle(ctx context.Context, userText string) (Result, error) {
	return a.handle(ctx, userText, true)
}

// HandleFresh executes one user task without shared conversation history.
func (a *Agent) HandleFresh(ctx context.Context, userText string) (Result, error) {
	return a.handle(ctx, userText, false)
}

func (a *Agent) handle(ctx context.Context, userText string, includeHistory bool) (Result, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.beginTask(); err != nil {
		return Result{Failed: true}, err
	}
	messages := []PromptMessage{{Role: "system", Content: a.system}}
	if includeHistory && a.cfg.History != nil {
		messages = append(messages, a.cfg.History()...)
	} else {
		messages = append(messages, PromptMessage{Role: "user", Content: userText})
	}
	historyEnd := len(messages)
	var prose strings.Builder
	promptTokens, completionTokens := 0, 0
	retriedEmptyCompletion := false
	var lastObservationHash [32]byte
	haveLastObservationHash := false
	for a.step < a.cfg.MaxSteps {
		a.status("planning")
		onDelta := func(delta string) {
			prose.WriteString(delta)
			if includeHistory {
				a.broadcast(map[string]any{"type": "chat-delta", "text": delta})
			}
		}
		prepared, reply, calls, usage, err := a.attemptCompletion(
			ctx, messages, historyEnd, onDelta, "",
		)
		messages = prepared.Messages
		historyEnd = prepared.HistoryEnd
		if errors.Is(err, errContextExceeded) {
			messages = compactAfterContextError(messages, historyEnd)
			historyEnd = min(2, len(messages))
			prepared, reply, calls, usage, err = a.attemptCompletion(
				ctx, messages, historyEnd, onDelta, "server overflow: hard compact",
			)
			messages = prepared.Messages
			historyEnd = prepared.HistoryEnd
		}
		if errors.Is(err, errContextExceeded) {
			var reduced bool
			messages, reduced = shrinkLatestObservation(messages, true)
			if reduced {
				prepared, reply, calls, usage, err = a.attemptCompletion(
					ctx, messages, historyEnd, onDelta,
					"server overflow: reduced observation and removed image",
				)
				messages = prepared.Messages
				historyEnd = prepared.HistoryEnd
			}
		}
		promptTokens += usage.PromptTokens
		completionTokens += usage.CompletionTokens
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
				a.status("stopped")
				return Result{
					Reply: prose.String(), Stopped: true,
					PromptTokens: promptTokens, CompletionTokens: completionTokens,
				}, nil
			}
			a.status("failed")
			return Result{
				Reply: prose.String(), Failed: true,
				PromptTokens: promptTokens, CompletionTokens: completionTokens,
			}, err
		}
		if len(calls) == 0 {
			if strings.TrimSpace(reply) == "" && strings.TrimSpace(prose.String()) == "" {
				if !retriedEmptyCompletion {
					retriedEmptyCompletion = true
					continue
				}
				const message = "I could not produce a response. Please try again."
				a.broadcast(map[string]any{"type": "chat-delta", "text": message})
				a.status("failed")
				return Result{
					Reply: message, Failed: true,
					PromptTokens: promptTokens, CompletionTokens: completionTokens,
				}, nil
			}
			if reply != "" && prose.Len() == 0 {
				prose.WriteString(reply)
			}
			a.status("done")
			return Result{
				Reply:        prose.String(),
				PromptTokens: promptTokens, CompletionTokens: completionTokens,
			}, nil
		}
		messages = append(messages, PromptMessage{Role: "assistant", Content: reply, ToolCalls: calls})
		observationMessages := make([]PromptMessage, 0)
		for _, call := range calls {
			if a.step >= a.cfg.MaxSteps {
				break
			}
			a.step++
			a.status("acting")
			if local, ok := a.tools.(*localTools); ok {
				local.stepID = fmt.Sprintf("%s-%d", a.taskID, a.step)
			}
			toolStarted := time.Now()
			result, toolErr := a.tools.Execute(ctx, call.Function.Name, json.RawMessage(call.Function.Arguments))
			if toolErr != nil {
				result.Text = "tool error: " + toolErr.Error()
			}
			if result.Summary == "" {
				result.Summary = call.Function.Name
			}
			thumbnail := a.recordStep(ctx, call, result, toolErr)
			if a.cfg.Counters != nil {
				a.cfg.Counters.AddAction(ActionCategory(call.Function.Name, result.Observe))
			}
			if a.cfg.Activity != nil {
				var args any
				if json.Unmarshal([]byte(call.Function.Arguments), &args) != nil {
					args = call.Function.Arguments
				}
				_ = a.cfg.Activity.Record(jobs.ActivityEvent{
					Kind: "tool", Name: call.Function.Name, JobID: jobs.JobID(ctx), Summary: result.Summary,
					Detail: jobs.ActivityDetail{
						Args: args, ResultText: result.Text, DurationMS: time.Since(toolStarted).Milliseconds(),
						OK: toolErr == nil, ScreenshotThumb: thumbnail,
					},
				})
			}
			toolContent := truncatePromptText(result.Text, toolTextCap)
			if result.Observe {
				toolContent = "Observation captured; use the following observation message."
			}
			messages = append(messages, PromptMessage{
				Role: "tool", ToolCallID: call.ID, Content: toolContent,
			})
			if result.Observe {
				observationMessages = append(observationMessages, makeObservationMessage(
					call, result, observationPromptCap(a.cfg.ContextTokens),
					&lastObservationHash, &haveLastObservationHash,
				))
			}
			if result.Observe {
				a.status("observing")
			}
		}
		messages = append(messages, observationMessages...)
	}
	message := fmt.Sprintf("I stopped after reaching the %d-step safety limit.", a.cfg.MaxSteps)
	if prose.Len() > 0 {
		message = prose.String() + "\n\n" + message
	} else {
		a.broadcast(map[string]any{"type": "chat-delta", "text": message})
	}
	a.status("failed")
	return Result{
		Reply: message, Failed: true,
		PromptTokens: promptTokens, CompletionTokens: completionTokens,
	}, nil
}

func truncatePromptText(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	if limit <= len(promptTruncationSuffix) {
		return text[:limit]
	}
	return text[:limit-len(promptTruncationSuffix)] + promptTruncationSuffix
}

func isObservationMessage(message PromptMessage) bool {
	_, ok := observationText(message)
	return ok
}

func compactTaskMessages(messages []PromptMessage, historyEnd int) []PromptMessage {
	if historyEnd >= len(messages) {
		return messages
	}
	roundStarts := toolRoundStarts(messages, historyEnd)
	keepFrom := historyEnd
	if len(roundStarts) > maxToolRounds {
		keepFrom = roundStarts[len(roundStarts)-maxToolRounds]
	}
	compacted := append([]PromptMessage(nil), messages[:historyEnd]...)
	latestObservation := -1
	latestUnchanged := -1
	for index := len(messages) - 1; index >= historyEnd; index-- {
		if isUnchangedObservation(messages[index]) {
			if latestUnchanged < 0 {
				latestUnchanged = index
			}
			continue
		}
		if isObservationMessage(messages[index]) {
			latestObservation = index
			break
		}
	}
	if latestObservation >= historyEnd && latestObservation < keepFrom {
		// Repeated unchanged observations can push the last full page state
		// outside the retained tool rounds. Keep that state without retaining
		// every intervening round.
		compacted = append(compacted, messages[latestObservation])
	}
	selectedObservations := map[int]bool{
		latestObservation: true,
		latestUnchanged:   true,
	}
	activeToolStubs := make(map[int]bool)
	retainedRoundStarts := toolRoundStarts(messages, keepFrom)
	for round, start := range retainedRoundStarts {
		end := len(messages)
		if round+1 < len(retainedRoundStarts) {
			end = retainedRoundStarts[round+1]
		}
		stubs := make([]int, 0)
		observations := make([]int, 0)
		for index := start; index < end; index++ {
			if messages[index].Role == "tool" && messages[index].Content == observationToolStub {
				stubs = append(stubs, index)
			}
			if isObservationMessage(messages[index]) {
				observations = append(observations, index)
			}
		}
		for index := 0; index < min(len(stubs), len(observations)); index++ {
			if selectedObservations[observations[index]] {
				activeToolStubs[stubs[index]] = true
			}
		}
	}
	for index := keepFrom; index < len(messages); index++ {
		message := messages[index]
		if isObservationMessage(message) && index != latestObservation && index != latestUnchanged {
			compacted = append(compacted, PromptMessage{Role: "user", Content: supersededObservation})
			continue
		}
		if message.Role == "tool" && message.Content == observationToolStub && !activeToolStubs[index] {
			message.Content = supersededToolStub
		}
		compacted = append(compacted, message)
	}
	return compacted
}

func compactAfterContextError(messages []PromptMessage, historyEnd int) []PromptMessage {
	if len(messages) == 0 {
		return messages
	}
	compacted := []PromptMessage{messages[0]}
	for index := min(historyEnd, len(messages)) - 1; index > 0; index-- {
		if messages[index].Role == "user" {
			compacted = append(compacted, messages[index])
			break
		}
	}
	latestRound := -1
	for index := len(messages) - 1; index >= historyEnd; index-- {
		if messages[index].Role == "assistant" && len(messages[index].ToolCalls) > 0 {
			latestRound = index
			break
		}
	}
	if latestRound >= 0 {
		latestRoundHasUnchanged := false
		for index := latestRound; index < len(messages); index++ {
			if isUnchangedObservation(messages[index]) {
				latestRoundHasUnchanged = true
				break
			}
		}
		if latestRoundHasUnchanged {
			for index := latestRound - 1; index >= historyEnd; index-- {
				if isObservationMessage(messages[index]) && !isUnchangedObservation(messages[index]) {
					compacted = append(compacted, messages[index])
					break
				}
			}
		}
		compacted = append(compacted, messages[latestRound:]...)
	}
	return compactTaskMessages(compacted, min(2, len(compacted)))
}

type streamedReply struct {
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Delta        struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
	Timings struct {
		PromptN     int     `json:"prompt_n"`
		PredictedN  int     `json:"predicted_n"`
		CacheN      int     `json:"cache_n"`
		PromptMs    float64 `json:"prompt_ms"`
		PredictedMs float64 `json:"predicted_ms"`
	} `json:"timings"`
}

type tokenUsage struct {
	PromptTokens     int
	CompletionTokens int
}

func (a *Agent) complete(ctx context.Context, messages []PromptMessage, maxTokens int, onDelta func(string)) (string, []ToolCall, tokenUsage, error) {
	definitions := a.toolDefinitions()
	body, err := json.Marshal(map[string]any{
		"stream": true, "messages": messages, "tools": definitions, "tool_choice": "auto",
		"max_tokens": maxTokens,
	})
	if err != nil {
		return "", nil, tokenUsage{}, fmt.Errorf("encode llama request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.LlamaURL, bytes.NewReader(body))
	if err != nil {
		return "", nil, tokenUsage{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := a.cfg.Client.Do(request)
	if err != nil {
		return "", nil, tokenUsage{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		if response.StatusCode == http.StatusBadRequest &&
			(strings.Contains(string(message), "exceed_context_size_error") ||
				strings.Contains(string(message), "context_length_exceeded")) {
			return "", nil, tokenUsage{}, errContextExceeded
		}
		return "", nil, tokenUsage{}, fmt.Errorf("llama returned %s: %s", response.Status, strings.TrimSpace(string(message)))
	}
	var text strings.Builder
	calls := make(map[int]*ToolCall)
	usage := tokenUsage{}
	cachedTokens := 0
	promptMs, predictedMs := 0.0, 0.0
	fallbackCompletionTokens := 0
	finishReason := ""
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		data, ok := strings.CutPrefix(scanner.Text(), "data: ")
		if !ok || strings.TrimSpace(data) == "" {
			continue
		}
		if strings.TrimSpace(data) == "[DONE]" {
			break
		}
		var chunk streamedReply
		if json.Unmarshal([]byte(data), &chunk) != nil {
			continue
		}
		if chunk.Timings.PromptN > 0 {
			usage.PromptTokens = chunk.Timings.PromptN
		}
		if chunk.Timings.PredictedN > 0 {
			usage.CompletionTokens = chunk.Timings.PredictedN
		}
		if chunk.Timings.CacheN > 0 {
			cachedTokens = chunk.Timings.CacheN
		}
		if chunk.Timings.PromptMs > 0 {
			promptMs = chunk.Timings.PromptMs
		}
		if chunk.Timings.PredictedMs > 0 {
			predictedMs = chunk.Timings.PredictedMs
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		if chunk.Choices[0].FinishReason != "" {
			finishReason = chunk.Choices[0].FinishReason
		}
		delta := chunk.Choices[0].Delta
		if delta.Content != "" {
			text.WriteString(delta.Content)
			onDelta(delta.Content)
			fallbackCompletionTokens++
		}
		for _, part := range delta.ToolCalls {
			call := calls[part.Index]
			if call == nil {
				call = &ToolCall{Type: "function"}
				calls[part.Index] = call
			}
			call.ID += part.ID
			if part.Type != "" {
				call.Type = part.Type
			}
			call.Function.Name += part.Function.Name
			call.Function.Arguments += part.Function.Arguments
		}
	}
	if err := scanner.Err(); err != nil {
		return text.String(), nil, usage, err
	}
	if finishReason == "length" {
		const marker = "\n…[response truncated at token limit]"
		text.WriteString(marker)
		onDelta(marker)
		log.Printf("agent completion reached max_tokens=%d", max(1, a.cfg.ContextTokens/4))
	}
	if usage.CompletionTokens == 0 {
		usage.CompletionTokens = fallbackCompletionTokens
	}
	if a.cfg.Counters != nil {
		a.cfg.Counters.AddLLM(usage.PromptTokens, usage.CompletionTokens, cachedTokens, promptMs, predictedMs)
	}
	ordered := make([]ToolCall, 0, len(calls))
	for index := 0; index < len(calls); index++ {
		if calls[index] != nil {
			if calls[index].ID == "" {
				calls[index].ID = "call-" + strconv.Itoa(index)
			}
			ordered = append(ordered, *calls[index])
		}
	}
	return text.String(), ordered, usage, nil
}

func encodeBase64(data []byte) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var out strings.Builder
	out.Grow((len(data) + 2) / 3 * 4)
	for index := 0; index < len(data); index += 3 {
		value := uint(data[index]) << 16
		remaining := len(data) - index
		if remaining > 1 {
			value |= uint(data[index+1]) << 8
		}
		if remaining > 2 {
			value |= uint(data[index+2])
		}
		out.WriteByte(alphabet[(value>>18)&63])
		out.WriteByte(alphabet[(value>>12)&63])
		if remaining > 1 {
			out.WriteByte(alphabet[(value>>6)&63])
		} else {
			out.WriteByte('=')
		}
		if remaining > 2 {
			out.WriteByte(alphabet[value&63])
		} else {
			out.WriteByte('=')
		}
	}
	return out.String()
}
