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
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultMaxSteps = 25
	defaultKeep     = 20
)

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
	LlamaURL    string
	CDPURL      string
	Display     string
	Resolution  string
	XdotoolPath string
	ScrotPath   string
	ConvertPath string
	BashPath    string
	DataDir     string
	Manifest    string
	MaxSteps    int
	KeepTasks   int
	Broadcast   func([]byte)
	History     func() []PromptMessage
	Client      *http.Client
	Runner      Runner
	Executor    Executor
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
}

// New constructs an agent and its default local tool executor.
func New(cfg Config) *Agent {
	if cfg.MaxSteps <= 0 {
		cfg.MaxSteps = defaultMaxSteps
	}
	if cfg.KeepTasks <= 0 {
		cfg.KeepTasks = defaultKeep
	}
	if cfg.Broadcast == nil {
		cfg.Broadcast = func([]byte) {}
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: 5 * time.Minute}
	}
	if cfg.DataDir == "" {
		cfg.DataDir = filepath.Join(os.TempDir(), "virtualme-agent")
	}
	if cfg.Manifest == "" {
		cfg.Manifest = "/opt/agent/system-manifest.json"
	}
	a := &Agent{cfg: cfg}
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
	return fmt.Sprintf(`You are Virtual Me, a concise private assistant running locally for one trusted user.
You can operate the visible Chromium window. Act only through the provided OS-input tools; CDP/DOM tools are observation-only.
Prefer DOM refs with click_element/type_into for precision. Use coordinate clicks only as fallback. Screenshots use %dx%d API coordinates mapped to a %dx%d display.
Use tools when the user asks you to operate or inspect the browser/system. For ordinary questions, answer directly without tools.
Stop as soon as the task is complete and report the result. Never claim an action succeeded unless an observation confirms it.
Environment manifest: %s`, apiWidth, apiHeight, width, height, environment)
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

// Handle executes one user task until the model returns a final answer.
func (a *Agent) Handle(ctx context.Context, userText string) (Result, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.beginTask(); err != nil {
		return Result{Failed: true}, err
	}
	messages := []PromptMessage{{Role: "system", Content: a.system}}
	if a.cfg.History != nil {
		messages = append(messages, a.cfg.History()...)
	} else {
		messages = append(messages, PromptMessage{Role: "user", Content: userText})
	}
	var prose strings.Builder
	promptTokens, completionTokens := 0, 0
	for a.step < a.cfg.MaxSteps {
		a.status("planning")
		reply, calls, usage, err := a.complete(ctx, messages, func(delta string) {
			prose.WriteString(delta)
			a.broadcast(map[string]any{"type": "chat-delta", "text": delta})
		})
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
			result, toolErr := a.tools.Execute(ctx, call.Function.Name, json.RawMessage(call.Function.Arguments))
			if toolErr != nil {
				result.Text = "tool error: " + toolErr.Error()
			}
			if result.Summary == "" {
				result.Summary = call.Function.Name
			}
			a.recordStep(ctx, call, result, toolErr)
			messages = append(messages, PromptMessage{
				Role: "tool", ToolCallID: call.ID, Content: result.Text,
			})
			if len(result.ImageJPEG) > 0 {
				observationMessages = append(observationMessages, PromptMessage{
					Role: "user",
					Content: []map[string]any{
						{"type": "text", "text": result.Text},
						{"type": "image_url", "image_url": map[string]string{
							"url": "data:image/jpeg;base64," + encodeBase64(result.ImageJPEG),
						}},
					},
				})
			} else if result.Observe {
				observationMessages = append(observationMessages, PromptMessage{
					Role: "user", Content: "Observation from " + call.Function.Name + ":\n" + result.Text,
				})
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

type streamedReply struct {
	Choices []struct {
		Delta struct {
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
		PromptN    int `json:"prompt_n"`
		PredictedN int `json:"predicted_n"`
	} `json:"timings"`
}

type tokenUsage struct {
	PromptTokens     int
	CompletionTokens int
}

func (a *Agent) complete(ctx context.Context, messages []PromptMessage, onDelta func(string)) (string, []ToolCall, tokenUsage, error) {
	definitions := make([]map[string]any, 0)
	for _, tool := range a.tools.Definitions() {
		definitions = append(definitions, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": tool.Name, "description": tool.Description, "parameters": tool.Schema,
			},
		})
	}
	body, _ := json.Marshal(map[string]any{
		"stream": true, "messages": messages, "tools": definitions, "tool_choice": "auto",
	})
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
		return "", nil, tokenUsage{}, fmt.Errorf("llama returned %s: %s", response.Status, strings.TrimSpace(string(message)))
	}
	var text strings.Builder
	calls := make(map[int]*ToolCall)
	usage := tokenUsage{}
	fallbackCompletionTokens := 0
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
		if len(chunk.Choices) == 0 {
			continue
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
	if usage.CompletionTokens == 0 {
		usage.CompletionTokens = fallbackCompletionTokens
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
