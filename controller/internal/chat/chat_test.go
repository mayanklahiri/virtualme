package chat

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mayanklahiri/virtualme/controller/internal/agent"
	"github.com/mayanklahiri/virtualme/controller/internal/jobs"
	"github.com/mayanklahiri/virtualme/controller/internal/valkey"
	"github.com/mayanklahiri/virtualme/controller/internal/ws"
	"github.com/mayanklahiri/virtualme/controller/prompts"
)

type respServer struct {
	addr     string
	commands chan []string
	close    func()
}

func readRESPCommand(reader *bufio.Reader) ([]string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	var count int
	if _, err := fmt.Sscanf(line, "*%d\r\n", &count); err != nil {
		return nil, err
	}
	result := make([]string, count)
	for index := range result {
		if _, err := reader.ReadString('\n'); err != nil {
			return nil, err
		}
		lengthLine, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		result[index] = strings.TrimSuffix(lengthLine, "\r\n")
	}
	return result, nil
}

func newRESPServer(t *testing.T) *respServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	commands := make(chan []string, 64)
	stats := map[string]int64{}
	done := make(chan struct{})
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				close(done)
				return
			}
			args, parseErr := readRESPCommand(bufio.NewReader(conn))
			commands <- args
			switch {
			case parseErr != nil:
				_, _ = io.WriteString(conn, "-ERR parse\r\n")
			case args[0] == "HINCRBY":
				value := int64(0)
				_, _ = fmt.Sscan(args[3], &value)
				stats[args[2]] += value
				fmt.Fprintf(conn, ":%d\r\n", stats[args[2]])
			case args[0] == "HGETALL":
				fmt.Fprintf(conn, "*8\r\n$7\r\nqueries\r\n$%d\r\n%d\r\n$12\r\npromptTokens\r\n$%d\r\n%d\r\n$16\r\ncompletionTokens\r\n$%d\r\n%d\r\n$5\r\ngenMs\r\n$%d\r\n%d\r\n",
					len(fmt.Sprint(stats["queries"])), stats["queries"],
					len(fmt.Sprint(stats["promptTokens"])), stats["promptTokens"],
					len(fmt.Sprint(stats["completionTokens"])), stats["completionTokens"],
					len(fmt.Sprint(stats["genMs"])), stats["genMs"])
			case args[0] == "LTRIM":
				_, _ = io.WriteString(conn, "+OK\r\n")
			case args[0] == "DEL":
				_, _ = io.WriteString(conn, ":2\r\n")
			default:
				_, _ = io.WriteString(conn, ":1\r\n")
			}
			_ = conn.Close()
		}
	}()
	server := &respServer{addr: listener.Addr().String(), commands: commands}
	server.close = func() {
		_ = listener.Close()
		<-done
	}
	t.Cleanup(server.close)
	return server
}

// sseServer streams the given deltas as OpenAI-style SSE, then [DONE].
// If hold is non-nil the handler blocks on it before streaming.
func sseServer(t *testing.T, deltas []string, hold chan struct{}) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hold != nil {
			<-hold
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, delta := range deltas {
			chunk, _ := json.Marshal(map[string]any{
				"choices": []map[string]any{{"delta": map[string]string{"content": delta}}},
			})
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	return server
}

func timedSSEServer(t *testing.T, delta string, promptTokens, completionTokens int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		content, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{{"delta": map[string]string{"content": delta}}},
		})
		timings, _ := json.Marshal(map[string]any{
			"timings": map[string]int{"prompt_n": promptTokens, "predicted_n": completionTokens},
		})
		fmt.Fprintf(w, "data: %s\n\ndata: %s\n\ndata: [DONE]\n\n", content, timings)
	}))
	t.Cleanup(server.Close)
	return server
}

func eventType(payload []byte) string {
	var event struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(payload, &event)
	return event.Type
}

func waitEvent(t *testing.T, events chan []byte, wantType string) []byte {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case payload := <-events:
			if got := eventType(payload); got == wantType {
				return payload
			} else if got == "llm-status" {
				continue
			} else {
				t.Fatalf("event %q arrived, want %q (payload %s)", got, wantType, payload)
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %q", wantType)
		}
	}
}

func TestStreamingRoundTrip(t *testing.T) {
	llama := sseServer(t, []string{"po", "n", "g"}, nil)
	events := make(chan []byte, 64)
	service := New("127.0.0.1:1", llama.URL, func(p []byte) { events <- p })

	service.HandleClientMessage(nil, []byte(`{"type":"chat","text":" hello "}`))

	message := waitEvent(t, events, "chat-message")
	if !strings.Contains(string(message), `"text":"hello"`) {
		t.Fatalf("chat-message = %s, want trimmed text", message)
	}
	var streamed strings.Builder
	for range 3 {
		var delta struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(waitEvent(t, events, "chat-delta"), &delta); err != nil {
			t.Fatal(err)
		}
		streamed.WriteString(delta.Text)
	}
	done := waitEvent(t, events, "chat-done")
	if streamed.String() != "pong" || !strings.Contains(string(done), `"text":"pong"`) {
		t.Fatalf("streamed %q, done %s", streamed.String(), done)
	}

	service.mu.Lock()
	defer service.mu.Unlock()
	if len(service.history) != 2 || service.history[0].Role != "user" || service.history[1].Role != "assistant" {
		t.Fatalf("history = %+v, want [user, assistant]", service.history)
	}
}

type noToolExecutor struct{}

func (noToolExecutor) Definitions() []agent.Tool { return nil }
func (noToolExecutor) Execute(context.Context, string, json.RawMessage) (agent.ToolResult, error) {
	return agent.ToolResult{}, nil
}

func TestAgentRoutingPersistsFinalReply(t *testing.T) {
	valkey := newRESPServer(t)
	llama := timedSSEServer(t, "agent reply", 23, 5)
	events := make(chan []byte, 64)
	service := NewWithAgent(valkey.addr, llama.URL, func(payload []byte) { events <- payload }, agent.Config{
		DataDir: t.TempDir(), Executor: noToolExecutor{},
	})
	service.HandleClientMessage(nil, []byte(`{"type":"chat","text":"use agent"}`))
	waitEvent(t, events, "chat-message")
	early := waitEvent(t, events, "chat-stats")
	if !strings.Contains(string(early), `"queries":1`) {
		t.Fatalf("early chat-stats = %s, want queries counted at submit", early)
	}
	waitEvent(t, events, "agent-status")
	waitEvent(t, events, "chat-delta")
	waitEvent(t, events, "agent-status")
	done := waitEvent(t, events, "chat-done")
	if !strings.Contains(string(done), `"text":"agent reply"`) {
		t.Fatalf("chat-done = %s", done)
	}
	stats := waitEvent(t, events, "chat-stats")
	if !strings.Contains(string(stats), `"queries":1`) ||
		!strings.Contains(string(stats), `"promptTokens":23`) ||
		!strings.Contains(string(stats), `"completionTokens":5`) {
		t.Fatalf("chat-stats = %s", stats)
	}
}

func TestBoundedHistoryLimitsCountAndTextSize(t *testing.T) {
	history := make([]Message, 20)
	for index := range history {
		history[index] = Message{
			Role: "user",
			Text: fmt.Sprintf("%02d:%s", index, strings.Repeat("x", 1020)),
		}
	}
	recent := boundedHistory(history)
	size := 0
	for _, message := range recent {
		size += len(message.Text)
	}
	if len(recent) >= contextWindow || size > historyPromptCap {
		t.Fatalf("bounded history has %d messages and %d bytes", len(recent), size)
	}
	if !strings.HasPrefix(recent[len(recent)-1].Text, "19:") {
		t.Fatalf("newest message was not retained: %+v", recent)
	}
}

func TestContextMessagesUsesEmbeddedPrompt(t *testing.T) {
	service := New("127.0.0.1:1", "http://127.0.0.1:1", func([]byte) {})
	service.history = []Message{{Role: "user", Text: "hello", Ts: 1}}
	messages := service.contextMessages(1)
	if len(messages) != 2 || messages[0]["role"] != "system" || messages[0]["content"] != prompts.Chat {
		t.Fatalf("context messages = %+v", messages)
	}
}

func TestInvalidInputPerConnectionError(t *testing.T) {
	events := make(chan []byte, 64)
	service := New("127.0.0.1:1", "http://127.0.0.1:1", func(p []byte) { events <- p })

	hub := ws.NewHub()
	hub.SetHandler(service.HandleClientMessage)
	server := httptest.NewServer(http.HandlerFunc(hub.HandleUpgrade))
	defer server.Close()
	conn, reader := dialWS(t, server.URL)
	defer conn.Close()

	for _, bad := range []string{
		"garbage",
		`{"type":"other","text":"x"}`,
		`{"type":"chat","text":""}`,
		fmt.Sprintf(`{"type":"chat","text":%q}`, strings.Repeat("x", 5000)),
	} {
		writeMaskedText(t, conn, bad)
		payload := readTextFrame(t, reader)
		if eventType(payload) != "chat-error" {
			t.Fatalf("input %q produced %s, want chat-error", bad, payload)
		}
	}
	select {
	case payload := <-events:
		t.Fatalf("unexpected broadcast %s", payload)
	default:
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if len(service.history) != 0 {
		t.Fatalf("history = %+v, want empty", service.history)
	}
}

func TestSecondChatIsAcceptedWithoutBusyError(t *testing.T) {
	hold := make(chan struct{})
	llama := sseServer(t, []string{"ok"}, hold)
	events := make(chan []byte, 64)
	service := New("127.0.0.1:1", llama.URL, func(p []byte) { events <- p })

	service.HandleClientMessage(nil, []byte(`{"type":"chat","text":"first"}`))
	waitEvent(t, events, "chat-message")
	service.HandleClientMessage(nil, []byte(`{"type":"chat","text":"second"}`))
	waitEvent(t, events, "chat-message")

	close(hold)
	deltas, dones := 0, 0
	for dones < 2 {
		payload := <-events
		switch eventType(payload) {
		case "chat-delta":
			deltas++
		case "chat-done":
			dones++
		}
	}
	if deltas != 2 {
		t.Fatalf("got %d deltas, want 2", deltas)
	}
}

func TestChatEnqueuesWithInitiatorAndQueuedFeedback(t *testing.T) {
	server := newRESPServer(t)
	events := make(chan []byte, 8)
	service := New(server.addr, "http://127.0.0.1:1", func(payload []byte) { events <- payload })
	manager := jobs.New(valkey.New(server.addr), nil)
	service.SetJobManager(manager)
	conn := &ws.Conn{}
	service.HandleClientMessage(conn, []byte(`{"type":"chat","text":"queued"}`))
	waitEvent(t, events, "chat-message")
	waitEvent(t, events, "chat-stats")
	status := waitEvent(t, events, "llm-status")
	if !strings.Contains(string(status), `"phase":"queued"`) || !strings.Contains(string(status), `"queued behind 2 jobs"`) {
		t.Fatalf("status = %s", status)
	}
	var pushed []string
	timeout := time.After(2 * time.Second)
	for pushed == nil {
		select {
		case command := <-server.commands:
			if command[0] == "RPUSH" && command[1] == "virtualme:jobs:ready:interactive" {
				pushed = command
			}
		case <-timeout:
			t.Fatal("ready-queue RPUSH never observed")
		}
	}
	if len(pushed) != 3 || pushed[1] != "virtualme:jobs:ready:interactive" {
		t.Fatalf("RPUSH = %v", pushed)
	}
	var env jobs.Envelope
	if err := json.Unmarshal([]byte(pushed[2]), &env); err != nil {
		t.Fatal(err)
	}
	if env.Type != "chat" || env.VisibilityTimeoutSec != 900 || !strings.Contains(string(env.Payload), `"text":"queued"`) {
		t.Fatalf("envelope = %+v", env)
	}
}

func TestLoadHistoryRetriesUntilValkeyResponds(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	// Not accepting yet: close so the first attempts get connection refused.
	_ = listener.Close()

	service := New(addr, "http://127.0.0.1:1", func([]byte) {})
	service.retryDelay = 10 * time.Millisecond

	entry, _ := json.Marshal(Message{Role: "user", Text: "persisted", Ts: 1})
	reply := fmt.Sprintf("*1\r\n$%d\r\n%s\r\n", len(entry), entry)
	go func() {
		time.Sleep(50 * time.Millisecond)
		revived, err := net.Listen("tcp", addr)
		if err != nil {
			return
		}
		defer revived.Close()
		conn, err := revived.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buffer := make([]byte, 512)
		_, _ = conn.Read(buffer)
		_, _ = io.WriteString(conn, reply)
	}()

	done := make(chan struct{})
	go func() {
		service.LoadHistory()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("LoadHistory never completed")
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if len(service.history) != 1 || service.history[0].Text != "persisted" {
		t.Fatalf("history = %+v, want the persisted message", service.history)
	}
}

func TestLlamaHTTPErrorBroadcastsAndFinishes(t *testing.T) {
	llama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer llama.Close()
	events := make(chan []byte, 64)
	service := New("127.0.0.1:1", llama.URL, func(p []byte) { events <- p })

	service.HandleClientMessage(nil, []byte(`{"type":"chat","text":"hi"}`))
	waitEvent(t, events, "chat-message")
	waitEvent(t, events, "chat-error")

	deadline := time.Now().Add(2 * time.Second)
	for {
		service.mu.Lock()
		running := service.fallbackCancel != nil
		service.mu.Unlock()
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fallback generation never finished after llama error")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStopPersistsPartialAndClearsBusy(t *testing.T) {
	firstDelta := make(chan struct{})
	llama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		flusher.Flush()
		close(firstDelta)
		<-r.Context().Done()
	}))
	defer llama.Close()
	events := make(chan []byte, 64)
	service := New("127.0.0.1:1", llama.URL, func(payload []byte) { events <- payload })
	service.HandleClientMessage(nil, []byte(`{"type":"chat","text":"long"}`))
	<-firstDelta
	for {
		payload := <-events
		if eventType(payload) == "chat-delta" {
			break
		}
	}
	service.HandleClientMessage(nil, []byte(`{"type":"chat-stop"}`))
	var done []byte
	deadline := time.After(5 * time.Second)
	for done == nil {
		select {
		case payload := <-events:
			if eventType(payload) == "chat-done" {
				done = payload
			}
		case <-deadline:
			t.Fatal("timed out waiting for stopped chat-done")
		}
	}
	if !strings.Contains(string(done), `"stopped":true`) || !strings.Contains(string(done), `"text":"partial"`) {
		t.Fatalf("chat-done = %s", done)
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if len(service.history) != 2 || service.history[1].Text != "partial" {
		t.Fatalf("history=%+v", service.history)
	}
}

func TestClearBroadcastsEmptyAndDeletesKeys(t *testing.T) {
	valkey := newRESPServer(t)
	events := make(chan []byte, 4)
	service := New(valkey.addr, "http://127.0.0.1:1", func(payload []byte) { events <- payload })
	service.history = []Message{{Role: "user", Text: "old"}}
	service.HandleClientMessage(nil, []byte(`{"type":"chat-clear"}`))
	command := <-valkey.commands
	if len(command) != 3 || command[0] != "DEL" || command[1] != historyKey || command[2] != statsKey {
		t.Fatalf("DEL command = %v", command)
	}
	if got := waitEvent(t, events, "chat-history"); !strings.Contains(string(got), `"messages":[]`) {
		t.Fatalf("history event = %s", got)
	}
	if got := waitEvent(t, events, "chat-stats"); !strings.Contains(string(got), `"queries":0`) {
		t.Fatalf("stats event = %s", got)
	}
}

func TestStatusSequenceAndTimingsStats(t *testing.T) {
	valkey := newRESPServer(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slots" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `[{"state":"prompt processing","prompt_n":3,"prompt_total":7}]`)
			return
		}
		time.Sleep(650 * time.Millisecond)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}],\"timings\":{\"prompt_n\":7,\"predicted_n\":2,\"predicted_per_second\":4}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	events := make(chan []byte, 128)
	service := New(valkey.addr, server.URL+"/v1/chat/completions", func(payload []byte) { events <- payload })
	service.HandleClientMessage(nil, []byte(`{"type":"chat","text":"status"}`))
	var phases []string
	var stats Stats
	deadline := time.After(5 * time.Second)
	for {
		select {
		case payload := <-events:
			switch eventType(payload) {
			case "llm-status":
				var status llmStatus
				_ = json.Unmarshal(payload, &status)
				if len(phases) == 0 || phases[len(phases)-1] != status.Phase {
					phases = append(phases, status.Phase)
				}
			case "chat-stats":
				_ = json.Unmarshal(payload, &stats)
				// Skip the submit-time stats bump; wait for the completion totals.
				if stats.Queries == 1 && stats.CompletionTokens > 0 {
					if strings.Join(phases, ",") != "sending,processing,generating" &&
						strings.Join(phases, ",") != "sending,processing,generating,idle" {
						t.Fatalf("phases = %v", phases)
					}
					if stats.PromptTokens != 7 || stats.CompletionTokens != 2 || stats.GenMS <= 0 {
						t.Fatalf("stats = %+v", stats)
					}
					return
				}
			}
		case <-deadline:
			t.Fatalf("timed out; phases=%v stats=%+v", phases, stats)
		}
	}
}

func TestSlotsUnavailableDoesNotFailChat(t *testing.T) {
	llama := sseServer(t, []string{"ok"}, nil)
	events := make(chan []byte, 64)
	service := New("127.0.0.1:1", llama.URL, func(payload []byte) { events <- payload })
	service.slotsURL = "http://127.0.0.1:1/slots"
	service.HandleClientMessage(nil, []byte(`{"type":"chat","text":"hi"}`))
	for {
		payload := <-events
		if eventType(payload) == "chat-done" {
			return
		}
		if eventType(payload) == "chat-error" {
			t.Fatalf("chat failed: %s", payload)
		}
	}
}

// --- raw websocket client helpers (mirrors internal/ws tests) ---

func dialWS(t *testing.T, serverURL string) (net.Conn, *bufio.Reader) {
	t.Helper()
	parsed, err := url.Parse(serverURL)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("tcp", parsed.Host)
	if err != nil {
		t.Fatal(err)
	}
	request := fmt.Sprintf("GET /ws HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n", parsed.Host)
	if _, err := io.WriteString(conn, request); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("handshake status = %d", response.StatusCode)
	}
	return conn, reader
}

func writeMaskedText(t *testing.T, conn net.Conn, message string) {
	t.Helper()
	mask := [4]byte{1, 2, 3, 4}
	frame := []byte{0x81}
	switch {
	case len(message) < 126:
		frame = append(frame, 0x80|byte(len(message)))
	case len(message) <= 65535:
		frame = append(frame, 0x80|126, byte(len(message)>>8), byte(len(message)))
	default:
		t.Fatal("test message too large")
	}
	frame = append(frame, mask[:]...)
	for index := range len(message) {
		frame = append(frame, message[index]^mask[index%4])
	}
	if _, err := conn.Write(frame); err != nil {
		t.Fatal(err)
	}
}

func readTextFrame(t *testing.T, reader *bufio.Reader) []byte {
	t.Helper()
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil {
		t.Fatal(err)
	}
	if header[0] != 0x81 {
		t.Fatalf("frame byte0 = %#x, want 0x81", header[0])
	}
	length := uint64(header[1] & 0x7f)
	switch header[1] & 0x7f {
	case 126:
		extended := make([]byte, 2)
		if _, err := io.ReadFull(reader, extended); err != nil {
			t.Fatal(err)
		}
		length = uint64(extended[0])<<8 | uint64(extended[1])
	case 127:
		t.Fatal("unexpected 64-bit length in test frame")
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		t.Fatal(err)
	}
	return payload
}
