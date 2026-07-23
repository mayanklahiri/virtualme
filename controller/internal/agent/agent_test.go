package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeExecutor struct {
	mu    sync.Mutex
	calls []string
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
			}}}})
			return
		}
		sse(w, map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": "finished"}}}})
	}))
	defer server.Close()
	executor := &fakeExecutor{}
	var events [][]byte
	agent := New(Config{
		LlamaURL: server.URL, DataDir: t.TempDir(), Executor: executor,
		Broadcast: func(payload []byte) { events = append(events, append([]byte(nil), payload...)) },
	})
	result, err := agent.Handle(context.Background(), "do it")
	if err != nil {
		t.Fatal(err)
	}
	if result.Reply != "finished" || result.Failed || result.Stopped {
		t.Fatalf("result = %+v", result)
	}
	if len(executor.calls) != 1 || executor.calls[0] != `echo:{"value":"ok"}` {
		t.Fatalf("calls = %v", executor.calls)
	}
	joined := string(bytesJoin(events))
	if !strings.Contains(joined, `"type":"agent-step"`) || !strings.Contains(joined, `"phase":"done"`) {
		t.Fatalf("events = %s", joined)
	}
	if _, err := os.Stat(filepath.Join(agent.taskDir, "steps.jsonl")); err != nil {
		t.Fatal("missing steps.jsonl:", err)
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
	tools := NewLocalTools(Config{DataDir: dataDir, BashPath: "bash"}).(*localTools)
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

func TestXdotoolInvocationUsesScreenCoordinates(t *testing.T) {
	runner := &recordingRunner{}
	tools := NewLocalTools(Config{
		Runner: runner, XdotoolPath: "/bin/xdotool", Display: ":77", Resolution: "1600x900x24",
	}).(*localTools)
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
