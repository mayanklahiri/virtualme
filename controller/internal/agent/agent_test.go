package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mayanklahiri/virtualme/controller/internal/jobs"
	"github.com/mayanklahiri/virtualme/controller/internal/tts"
)

type fakeExecutor struct {
	mu    sync.Mutex
	calls []string
}

type activityRecorder struct {
	events []jobs.ActivityEvent
}

func (recorder *activityRecorder) Record(event jobs.ActivityEvent) error {
	recorder.events = append(recorder.events, event)
	return nil
}

func TestAgentSystemPromptGolden(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(manifestPath, []byte(`{"os":"test"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got := buildSystemPrompt(Config{Manifest: manifestPath, Resolution: "1600x900x24"})
	if strings.Contains(got, "{{") {
		t.Fatalf("interpolated prompt contains an unresolved placeholder: %q", got)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "agent-system.golden"))
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("interpolated agent prompt changed (-want +got):\nwant:\n%q\ngot:\n%q", want, got)
	}
}

func TestSpeakToolDefinitionAndExecution(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintln(w, `{"type":"start","sampleRate":22050,"channels":1,"sentences":1}`)
		_, _ = fmt.Fprintln(w, `{"type":"chunk","seq":0,"pcm":"AQI="}`)
		_, _ = fmt.Fprintln(w, `{"type":"done","audioSec":1.2,"rtf":0.1}`)
	}))
	defer server.Close()
	var frames [][]byte
	activity := new(activityRecorder)
	tools := NewLocalTools(Config{
		DataDir: t.TempDir(), TTS: &tts.Client{URL: server.URL},
		Broadcast: func(payload []byte) { frames = append(frames, append([]byte(nil), payload...)) },
		Activity:  activity,
	})
	tools.resetTask("task-1")
	found := false
	for _, definition := range tools.Definitions() {
		if definition.Name == "speak" {
			found = true
		}
	}
	if !found {
		t.Fatal("speak definition missing")
	}
	result, err := tools.Execute(context.Background(), "speak", json.RawMessage(`{"text":"Hello aloud","speed":1}`))
	if err != nil || result.Text != `{"audioSec":1.2,"ok":true}` {
		t.Fatalf("Execute(speak) = %+v, %v", result, err)
	}
	joined := string(bytes.Join(frames, []byte("\n")))
	for _, want := range []string{`"type":"tts-start"`, `"type":"tts-chunk"`, `"type":"tts-done"`, `"origin":"chat"`} {
		if !strings.Contains(joined, want) {
			t.Errorf("frames missing %s: %s", want, joined)
		}
	}
	if len(activity.events) != 1 || activity.events[0].Kind != "tts" || activity.events[0].Detail.Chars != 11 {
		t.Fatalf("tts activity = %+v", activity.events)
	}
}

func (f *fakeExecutor) Definitions() []Tool {
	return []Tool{{Name: "echo", Description: "echo", Schema: schema(`{"type":"object"}`)}}
}

func (f *fakeExecutor) Execute(_ context.Context, name string, args json.RawMessage) (ToolResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name+":"+string(args))
	return ToolResult{Text: "tool result", Summary: "Echoed"}, nil
}

func sse(w http.ResponseWriter, chunks ...any) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, chunk := range chunks {
		encoded, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", encoded)
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
}

func TestDefaultLLMClientUsesContextCancellationWithoutWallClockTimeout(t *testing.T) {
	agent := New(Config{Executor: noDefinitionsExecutor{}})
	if agent.cfg.Client.Timeout != 0 {
		t.Fatalf("default HTTP timeout = %s, want context-controlled", agent.cfg.Client.Timeout)
	}
}

func TestToolLoopThenFinalReply(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		if requests == 1 {
			sse(w, map[string]any{"choices": []any{map[string]any{"delta": map[string]any{
				"tool_calls": []any{map[string]any{
					"index": 0, "id": "call-1", "type": "function",
					"function": map[string]string{"name": "echo", "arguments": `{"value":"ok"}`},
				}},
			}}}}, map[string]any{"timings": map[string]int{"prompt_n": 11, "predicted_n": 3}})
			return
		}
		sse(w,
			map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": "finished"}}}},
			map[string]any{"timings": map[string]int{"prompt_n": 17, "predicted_n": 4}},
		)
	}))
	defer server.Close()
	executor := &fakeExecutor{}
	activity := new(activityRecorder)
	var events [][]byte
	agent := New(Config{
		LlamaURL: server.URL, DataDir: t.TempDir(), Executor: executor,
		Broadcast: func(payload []byte) { events = append(events, append([]byte(nil), payload...)) },
		Activity:  activity,
	})
	result, err := agent.Handle(context.Background(), "do it")
	if err != nil {
		t.Fatal(err)
	}
	if result.Reply != "finished" || result.Failed || result.Stopped {
		t.Fatalf("result = %+v", result)
	}
	if result.PromptTokens != 28 || result.CompletionTokens != 7 {
		t.Fatalf("token usage = %d prompt + %d completion, want 28 + 7",
			result.PromptTokens, result.CompletionTokens)
	}
	if len(executor.calls) != 1 || executor.calls[0] != `echo:{"value":"ok"}` {
		t.Fatalf("calls = %v", executor.calls)
	}
	if len(activity.events) != 1 || activity.events[0].Kind != "tool" ||
		activity.events[0].Name != "echo" || activity.events[0].Detail.ResultText != "tool result" ||
		!activity.events[0].Detail.OK {
		t.Fatalf("activity = %+v", activity.events)
	}
	joined := string(bytesJoin(events))
	if !strings.Contains(joined, `"type":"agent-step"`) || !strings.Contains(joined, `"phase":"done"`) {
		t.Fatalf("events = %s", joined)
	}
	steps, err := os.ReadFile(filepath.Join(agent.taskDir, "steps.jsonl"))
	if err != nil {
		t.Fatal("missing steps.jsonl:", err)
	}
	if !strings.Contains(string(steps), `"text":"tool result"`) {
		t.Fatalf("steps.jsonl must persist observation text: %s", steps)
	}
	if !strings.Contains(joined, `"text":"tool result"`) {
		t.Fatal("agent-step frames must carry observation text")
	}
}

func TestCompletionBudgetAndLengthMarker(t *testing.T) {
	var maxTokens int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			MaxTokens int `json:"max_tokens"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		maxTokens = request.MaxTokens
		sse(w,
			map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": "partial"}}}},
			map[string]any{"choices": []any{map[string]any{"delta": map[string]any{}, "finish_reason": "length"}}},
		)
	}))
	defer server.Close()
	var events [][]byte
	agent := New(Config{
		LlamaURL: server.URL, DataDir: t.TempDir(), Executor: noDefinitionsExecutor{},
		ContextTokens: 32768,
		Broadcast:     func(payload []byte) { events = append(events, append([]byte(nil), payload...)) },
	})
	result, err := agent.Handle(context.Background(), "answer")
	if err != nil {
		t.Fatal(err)
	}
	if maxTokens != 8192 {
		t.Fatalf("max_tokens = %d, want 8192", maxTokens)
	}
	if result.Reply != "partial\n…[response truncated at token limit]" {
		t.Fatalf("reply = %q", result.Reply)
	}
	if !strings.Contains(string(bytesJoin(events)), "response truncated at token limit") {
		t.Fatal("length marker was not streamed")
	}
}

func TestAgentStepReplayBuffersLatestTask(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		if requests == 1 {
			sse(w, map[string]any{"choices": []any{map[string]any{"delta": map[string]any{
				"tool_calls": []any{map[string]any{
					"index": 0, "id": "call-1", "type": "function",
					"function": map[string]string{"name": "echo", "arguments": `{"value":"ok"}`},
				}},
			}}}})
			return
		}
		sse(w, map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": "finished"}}}})
	}))
	defer server.Close()
	agent := New(Config{LlamaURL: server.URL, DataDir: t.TempDir(), Executor: &fakeExecutor{}})
	if _, err := agent.Handle(context.Background(), "do it"); err != nil {
		t.Fatal(err)
	}
	frames := agent.ReplayFrames()
	if len(frames) != 1 {
		t.Fatalf("replay frames after tool task = %d, want 1", len(frames))
	}
	if !strings.Contains(string(frames[0]), `"type":"agent-step"`) {
		t.Fatalf("replay frame is not an agent-step: %s", frames[0])
	}
	// A second task without tool calls resets the buffer to that task's steps.
	if _, err := agent.Handle(context.Background(), "again"); err != nil {
		t.Fatal(err)
	}
	if frames := agent.ReplayFrames(); len(frames) != 0 {
		t.Fatalf("replay frames after tool-free task = %d, want 0", len(frames))
	}
}

func TestEmptyCompletionRetriesOnce(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		if requests == 1 {
			sse(w)
			return
		}
		sse(w, map[string]any{"choices": []any{
			map[string]any{"delta": map[string]string{"content": "recovered"}},
		}})
	}))
	defer server.Close()

	agent := New(Config{
		LlamaURL: server.URL,
		DataDir:  t.TempDir(),
		Executor: noDefinitionsExecutor{},
	})
	result, err := agent.Handle(context.Background(), "answer")
	if err != nil || result.Reply != "recovered" || result.Failed || requests != 2 {
		t.Fatalf("result=%+v err=%v requests=%d", result, err, requests)
	}
}

func TestHandleFreshExcludesConfiguredHistory(t *testing.T) {
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		sse(w, map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": "fresh"}}}})
	}))
	defer server.Close()
	agent := New(Config{
		LlamaURL: server.URL, DataDir: t.TempDir(), Executor: &fakeExecutor{},
		History: func() []PromptMessage {
			return []PromptMessage{{Role: "user", Content: "shared-history-secret"}}
		},
	})
	result, err := agent.HandleFresh(context.Background(), "isolated-project-task")
	if err != nil || result.Reply != "fresh" {
		t.Fatalf("result = %+v, %v", result, err)
	}
	if strings.Contains(string(body), "shared-history-secret") || !strings.Contains(string(body), "isolated-project-task") {
		t.Fatalf("request history was not isolated: %s", body)
	}
}

func TestStepCapFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		sse(w, map[string]any{"choices": []any{map[string]any{"delta": map[string]any{
			"tool_calls": []any{map[string]any{
				"index": 0, "id": "x", "function": map[string]string{"name": "echo", "arguments": `{}`},
			}},
		}}}})
	}))
	defer server.Close()
	agent := New(Config{LlamaURL: server.URL, DataDir: t.TempDir(), Executor: &fakeExecutor{}, MaxSteps: 2})
	result, err := agent.Handle(context.Background(), "loop")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Failed || !strings.Contains(result.Reply, "2-step safety limit") {
		t.Fatalf("result = %+v", result)
	}
}

func TestPromptCompactionBoundsToolRoundsAndObservations(t *testing.T) {
	messages := []PromptMessage{
		{Role: "system", Content: "system"},
		{Role: "user", Content: "task"},
	}
	for round := range 6 {
		messages = append(messages,
			PromptMessage{
				Role: "assistant",
				ToolCalls: []ToolCall{{
					ID:       fmt.Sprintf("call-%d", round),
					Function: FunctionCall{Name: "dom", Arguments: `{}`},
				}},
			},
			PromptMessage{Role: "tool", ToolCallID: fmt.Sprintf("call-%d", round), Content: "captured"},
			PromptMessage{Role: "user", Content: fmt.Sprintf("Observation from dom:\nround %d", round)},
		)
	}
	compacted := compactTaskMessages(messages, 2)
	rounds, observations := 0, 0
	for _, message := range compacted {
		if message.Role == "assistant" && len(message.ToolCalls) > 0 {
			rounds++
		}
		if isObservationMessage(message) {
			observations++
			if !strings.Contains(fmt.Sprint(message.Content), "round 5") {
				t.Fatalf("stale observation retained: %+v", message)
			}
		}
	}
	if rounds != maxToolRounds || observations != 1 {
		t.Fatalf("compacted to %d rounds and %d observations: %+v", rounds, observations, compacted)
	}
	if compacted[0].Content != "system" || compacted[1].Content != "task" {
		t.Fatalf("base conversation was not preserved: %+v", compacted[:2])
	}
}

func TestPromptTextTruncation(t *testing.T) {
	text := strings.Repeat("x", observationTextCap+100)
	got := truncatePromptText(text, observationTextCap)
	if len(got) != observationTextCap || !strings.Contains(got, "[truncated to fit model context]") {
		t.Fatalf("truncated text length=%d suffix=%q", len(got), got[len(got)-40:])
	}
}

func TestContextOverflowCompactsAndRetries(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		if requests == 1 {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"type":"exceed_context_size_error"}}`)
			return
		}
		sse(w, map[string]any{"choices": []any{
			map[string]any{"delta": map[string]string{"content": "recovered"}},
		}})
	}))
	defer server.Close()
	agent := New(Config{
		LlamaURL: server.URL,
		DataDir:  t.TempDir(),
		Executor: noDefinitionsExecutor{},
		History: func() []PromptMessage {
			return []PromptMessage{
				{Role: "user", Content: "old"},
				{Role: "assistant", Content: strings.Repeat("large", 5000)},
				{Role: "user", Content: "current"},
			}
		},
	})
	result, err := agent.Handle(context.Background(), "current")
	if err != nil || result.Reply != "recovered" || requests != 2 {
		t.Fatalf("result=%+v err=%v requests=%d", result, err, requests)
	}
}

type noDefinitionsExecutor struct{}

func (noDefinitionsExecutor) Definitions() []Tool { return nil }
func (noDefinitionsExecutor) Execute(context.Context, string, json.RawMessage) (ToolResult, error) {
	return ToolResult{}, nil
}

func TestContextCancelStops(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		<-release
	}))
	defer func() {
		close(release)
		server.Close()
	}()
	agent := New(Config{LlamaURL: server.URL, DataDir: t.TempDir(), Executor: &fakeExecutor{}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan Result, 1)
	go func() {
		result, _ := agent.Handle(ctx, "wait")
		done <- result
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case result := <-done:
		if !result.Stopped {
			t.Fatalf("result = %+v", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("agent did not stop")
	}
}

func TestCoordinateMappingRoundTrip(t *testing.T) {
	width, height := 1600, 900
	apiWidth, apiHeight := apiDimensions(width, height)
	for _, point := range [][2]float64{{0, 0}, {512, 288}, {1023, 575}} {
		x, y := apiToScreen(point[0], point[1], width, height, apiWidth, apiHeight)
		backX := float64(x) * float64(apiWidth) / float64(width)
		backY := float64(y) * float64(apiHeight) / float64(height)
		if abs(backX-point[0]) > 1 || abs(backY-point[1]) > 1 {
			t.Fatalf("%v mapped to %d,%d and back to %.2f,%.2f", point, x, y, backX, backY)
		}
	}
}

func TestDOMCompactionAllowlistRefsAndPagination(t *testing.T) {
	snapshot := snapshotResult{
		Strings:   []string{"HTML", "BUTTON", "id", "submit", "onclick", "bad()", "block", "visible", "Click me"},
		Documents: []snapshotDocument{{}},
	}
	document := &snapshot.Documents[0]
	document.Nodes.NodeName = []int{0, 1, 0}
	document.Nodes.NodeType = []int{1, 1, 3}
	document.Nodes.NodeValue = []int{0, 0, 8}
	document.Nodes.ParentIndex = []int{-1, 0, 1}
	document.Nodes.Attributes = [][]int{nil, {2, 3, 4, 5}, nil}
	document.Layout.NodeIndex = []int{0, 1}
	document.Layout.Bounds = [][]float64{{0, 0, 1600, 900}, {10, 20, 100, 40}}
	document.Layout.Styles = [][]int{{6, 7}, {6, 7}}
	elements, boxes := compactSnapshot(snapshot)
	if len(elements) != 2 || elements[1].Ref != 1 || elements[1].Attributes["id"] != "submit" {
		t.Fatalf("elements = %+v", elements)
	}
	if _, exists := elements[1].Attributes["onclick"]; exists {
		t.Fatal("unsafe attribute was retained")
	}
	if boxes[1] != [4]float64{10, 20, 100, 40} {
		t.Fatalf("box = %v", boxes[1])
	}
	page := paginateDOM(append(elements, elements...), 0, 180)
	if _, ok := page["more"]; !ok {
		t.Fatalf("pagination missing: %+v", page)
	}
}

func TestBashDenylistAndCwdEnvironmentPersistence(t *testing.T) {
	for _, command := range []string{
		"rm -rf /", "rm -fr -- /*", "mkfs.ext4 /dev/x", "dd if=/dev/zero of=/dev/sda",
		"echo x >/dev/nvme0n1", ":(){:|:&};:",
	} {
		if !commandDenied(command) {
			t.Fatalf("command not denied: %q", command)
		}
	}
	dataDir := t.TempDir()
	tools := NewLocalTools(Config{DataDir: dataDir, BashPath: "bash"})
	if _, err := tools.Execute(context.Background(), "bash", json.RawMessage(`{"command":"mkdir sub && cd sub && export VM_TEST_VALUE=kept"}`)); err != nil {
		t.Fatal(err)
	}
	result, err := tools.Execute(context.Background(), "bash", json.RawMessage(`{"command":"printf '%s:%s' \"$PWD\" \"$VM_TEST_VALUE\""}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Text, filepath.Join(dataDir, "sub")+":kept") {
		t.Fatalf("result = %q", result.Text)
	}
}

type recordingRunner struct {
	name string
	args []string
	env  []string
}

func (r *recordingRunner) Run(_ context.Context, name string, args, env []string, _ string) ([]byte, []byte, error) {
	r.name, r.args, r.env = name, append([]string(nil), args...), append([]string(nil), env...)
	return nil, nil, nil
}

type captureRunner struct {
	calls [][]string
}

func (r *captureRunner) Run(_ context.Context, name string, args, env []string, _ string) ([]byte, []byte, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	if name == "/bin/convert" {
		_ = os.WriteFile(args[len(args)-1], []byte("jpg"), 0o600)
	}
	return nil, nil, nil
}

func (r *captureRunner) convertArgs(t *testing.T) []string {
	t.Helper()
	for _, call := range r.calls {
		if call[0] == "/bin/convert" {
			return call[1:]
		}
	}
	t.Fatal("convert was never invoked")
	return nil
}

func TestManualScreenshotSkipsGridAgentKeepsIt(t *testing.T) {
	runner := &captureRunner{}
	tools := NewLocalTools(Config{
		Runner: runner, ScrotPath: "/bin/scrot", ConvertPath: "/bin/convert",
		Display: ":77", Resolution: "1600x900x24",
	})
	if _, err := tools.ExecuteManual(context.Background(), "screenshot", nil); err != nil {
		t.Fatal(err)
	}
	if manual := runner.convertArgs(t); slices.Contains(manual, "-draw") {
		t.Fatalf("manual capture must not draw a grid: %v", manual)
	}
	runner.calls = nil
	if _, err := tools.Execute(context.Background(), "screenshot", nil); err != nil {
		t.Fatal(err)
	}
	if vision := runner.convertArgs(t); !slices.Contains(vision, "-draw") {
		t.Fatalf("agent-vision capture must keep the grid: %v", vision)
	}
}

func TestXdotoolInvocationUsesScreenCoordinates(t *testing.T) {
	runner := &recordingRunner{}
	tools := NewLocalTools(Config{
		Runner: runner, XdotoolPath: "/bin/xdotool", Display: ":77", Resolution: "1600x900x24",
	})
	if _, err := tools.Execute(context.Background(), "click", json.RawMessage(`{"x":512,"y":288}`)); err != nil {
		t.Fatal(err)
	}
	if runner.name != "/bin/xdotool" || strings.Join(runner.args, " ") != "mousemove 800 450 click 1" ||
		strings.Join(runner.env, " ") != "DISPLAY=:77" {
		t.Fatalf("runner = %s %v env=%v", runner.name, runner.args, runner.env)
	}
}

func bytesJoin(parts [][]byte) []byte {
	var result []byte
	for _, part := range parts {
		result = append(result, part...)
	}
	return result
}

func abs(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
