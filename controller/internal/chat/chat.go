// Package chat implements the shared conversation: one history for the whole
// instance, streamed from llama-server and persisted in Valkey.
package chat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/mayanklahiri/virtualme/controller/internal/agent"
	"github.com/mayanklahiri/virtualme/controller/internal/ws"
)

const (
	historyKey    = "virtualme:chat"
	statsKey      = "virtualme:chat-stats"
	historyCap    = 200
	contextWindow = 16
	maxTextLen    = 4096
	systemPrompt  = "You are Virtual Me, a private assistant running locally inside the user's own container."
)

// Message is one chat history entry.
type Message struct {
	Role string `json:"role"`
	Text string `json:"text"`
	Ts   int64  `json:"ts"`
}

// Service owns the shared conversation state.
type Service struct {
	valkey     *valkeyClient
	llamaURL   string
	slotsURL   string
	broadcast  func([]byte)
	client     *http.Client
	retryDelay time.Duration
	agent      *agent.Agent

	mu      sync.Mutex
	history []Message
	busy    bool
	cancel  context.CancelFunc
}

// Stats contains persisted conversation totals.
type Stats struct {
	Queries          int64 `json:"queries"`
	PromptTokens     int64 `json:"promptTokens"`
	CompletionTokens int64 `json:"completionTokens"`
	GenMS            int64 `json:"genMs"`
}

// New creates a chat service; broadcast sends a text frame to every client.
func New(valkeyAddr, llamaURL string, broadcast func([]byte)) *Service {
	slotsURL := llamaURL
	if parsed, err := url.Parse(llamaURL); err == nil {
		parsed.Path = "/slots"
		parsed.RawQuery = ""
		slotsURL = parsed.String()
	}
	return &Service{
		valkey:     newValkeyClient(valkeyAddr),
		llamaURL:   llamaURL,
		slotsURL:   slotsURL,
		broadcast:  broadcast,
		client:     &http.Client{Timeout: 120 * time.Second},
		retryDelay: 2 * time.Second,
	}
}

// NewWithAgent creates the production chat service with browser-agent routing.
func NewWithAgent(valkeyAddr, llamaURL string, broadcast func([]byte), agentConfig agent.Config) *Service {
	service := New(valkeyAddr, llamaURL, broadcast)
	agentConfig.LlamaURL = llamaURL
	agentConfig.Broadcast = broadcast
	agentConfig.History = func() []agent.PromptMessage {
		service.mu.Lock()
		defer service.mu.Unlock()
		recent := service.history
		if len(recent) > contextWindow {
			recent = recent[len(recent)-contextWindow:]
		}
		messages := make([]agent.PromptMessage, 0, len(recent))
		for _, message := range recent {
			messages = append(messages, agent.PromptMessage{Role: message.Role, Content: message.Text})
		}
		return messages
	}
	service.agent = agent.New(agentConfig)
	return service
}

// LoadHistory populates memory from Valkey, retrying until Valkey responds so
// a controller that boots before Valkey still recovers the conversation.
// Run it in a goroutine; a permanently-down Valkey only costs a warning log.
func (s *Service) LoadHistory() {
	for attempt := 0; ; attempt++ {
		entries, err := s.valkey.lrange(historyKey, 0, -1)
		if err == nil {
			s.adoptHistory(entries)
			return
		}
		if attempt == 0 {
			log.Println("chat: history unavailable, retrying:", err)
		}
		time.Sleep(s.retryDelay)
	}
}

// adoptHistory prepends persisted messages ahead of anything that arrived in
// memory while the load was still retrying.
func (s *Service) adoptHistory(entries []string) {
	loaded := make([]Message, 0, len(entries))
	for _, entry := range entries {
		var message Message
		if json.Unmarshal([]byte(entry), &message) == nil {
			loaded = append(loaded, message)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history = append(loaded, s.history...)
	if len(s.history) > historyCap {
		s.history = s.history[len(s.history)-historyCap:]
	}
}

// HistoryMessage marshals the conversation for per-connection replay.
func (s *Service) HistoryMessage() []byte {
	s.mu.Lock()
	messages := make([]Message, len(s.history))
	copy(messages, s.history)
	s.mu.Unlock()
	payload, _ := json.Marshal(struct {
		Type     string    `json:"type"`
		Messages []Message `json:"messages"`
	}{Type: "chat-history", Messages: messages})
	return payload
}

func (s *Service) readStats() Stats {
	values, err := s.valkey.hgetall(statsKey)
	if err != nil {
		return Stats{}
	}
	return Stats{
		Queries: values["queries"], PromptTokens: values["promptTokens"],
		CompletionTokens: values["completionTokens"], GenMS: values["genMs"],
	}
}

// StatsMessage returns current totals, or zeros while Valkey is unavailable.
func (s *Service) StatsMessage() []byte {
	stats := s.readStats()
	return marshalEvent(struct {
		Type string `json:"type"`
		Stats
	}{Type: "chat-stats", Stats: stats})
}

// append records a message in memory and best-effort in Valkey.
func (s *Service) append(message Message) {
	s.mu.Lock()
	s.history = append(s.history, message)
	if len(s.history) > historyCap {
		s.history = s.history[len(s.history)-historyCap:]
	}
	s.mu.Unlock()

	encoded, _ := json.Marshal(message)
	if err := s.valkey.rpush(historyKey, string(encoded)); err != nil {
		log.Println("chat: persist failed:", err)
		return
	}
	if err := s.valkey.ltrim(historyKey, -historyCap, -1); err != nil {
		log.Println("chat: trim failed:", err)
	}
}

func marshalEvent(value any) []byte {
	payload, _ := json.Marshal(value)
	return payload
}

func errorEvent(text string) []byte {
	return marshalEvent(struct {
		Type  string `json:"type"`
		Error string `json:"error"`
	}{Type: "chat-error", Error: text})
}

func writeError(c *ws.Conn, text string) {
	if c != nil {
		_ = c.WriteText(errorEvent(text))
	}
}

// HandleClientMessage processes one client text frame.
func (s *Service) HandleClientMessage(c *ws.Conn, payload []byte) {
	var request struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(payload, &request) != nil {
		writeError(c, "unrecognized message")
		return
	}
	if request.Type == "chat-stop" {
		s.mu.Lock()
		cancel := s.cancel
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		return
	}
	if request.Type == "chat-clear" {
		s.clear(c)
		return
	}
	if request.Type != "chat" {
		writeError(c, "unrecognized message")
		return
	}
	text := strings.TrimSpace(request.Text)
	if text == "" || len(text) > maxTextLen {
		writeError(c, "message must be 1-4096 characters")
		return
	}

	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		writeError(c, "busy: a reply is already streaming")
		return
	}
	s.busy = true
	s.mu.Unlock()

	userMessage := Message{Role: "user", Text: text, Ts: time.Now().UnixMilli()}
	s.append(userMessage)
	s.broadcast(marshalEvent(struct {
		Type string `json:"type"`
		Message
	}{Type: "chat-message", Message: userMessage}))

	go s.generate()
}

func (s *Service) clear(c *ws.Conn) {
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		writeError(c, "busy: stop generation first")
		return
	}
	s.history = nil
	s.mu.Unlock()
	if err := s.valkey.del(historyKey, statsKey); err != nil {
		log.Println("chat: clear persist failed:", err)
	}
	s.broadcast(marshalEvent(struct {
		Type     string    `json:"type"`
		Messages []Message `json:"messages"`
	}{Type: "chat-history", Messages: []Message{}}))
	s.broadcast(marshalEvent(struct {
		Type string `json:"type"`
		Stats
	}{Type: "chat-stats", Stats: Stats{}}))
}

func (s *Service) clearBusy() {
	s.mu.Lock()
	s.busy = false
	s.cancel = nil
	s.mu.Unlock()
}

// contextMessages returns the OpenAI-style message list for the completion.
func (s *Service) contextMessages() []map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	recent := s.history
	if len(recent) > contextWindow {
		recent = recent[len(recent)-contextWindow:]
	}
	messages := make([]map[string]string, 0, len(recent)+1)
	messages = append(messages, map[string]string{"role": "system", "content": systemPrompt})
	for _, message := range recent {
		messages = append(messages, map[string]string{"role": message.Role, "content": message.Text})
	}
	return messages
}

type llmStatus struct {
	Type        string  `json:"type"`
	Phase       string  `json:"phase"`
	PromptN     int     `json:"promptN"`
	PromptTotal int     `json:"promptTotal"`
	Tokens      int     `json:"tokens"`
	TokPerSec   float64 `json:"tokPerSec"`
	ElapsedMS   int64   `json:"elapsedMs"`
}

type flight struct {
	mu          sync.Mutex
	phase       string
	promptN     int
	promptTotal int
	tokenTimes  []time.Time
	started     time.Time
}

func (s *Service) statusEvent(f *flight) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-5 * time.Second)
	first := 0
	for first < len(f.tokenTimes) && f.tokenTimes[first].Before(cutoff) {
		first++
	}
	recent := f.tokenTimes[first:]
	rate := float64(len(recent)) / 5
	if elapsed := now.Sub(f.started).Seconds(); elapsed < 5 && elapsed > 0 {
		rate = float64(len(recent)) / elapsed
	}
	return marshalEvent(llmStatus{
		Type: "llm-status", Phase: f.phase, PromptN: f.promptN,
		PromptTotal: f.promptTotal, Tokens: len(f.tokenTimes),
		TokPerSec: rate, ElapsedMS: time.Since(f.started).Milliseconds(),
	})
}

func (s *Service) pollSlot(ctx context.Context, f *flight) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.slotsURL, nil)
	if err != nil {
		return
	}
	response, err := s.client.Do(request)
	if err != nil {
		return
	}
	defer response.Body.Close()
	var slots []struct {
		State       string `json:"state"`
		PromptN     int    `json:"prompt_n"`
		PromptTotal int    `json:"prompt_total"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&slots) != nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.phase == "generating" {
		return
	}
	f.phase = "queued"
	for _, slot := range slots {
		if slot.PromptTotal > 0 || slot.PromptN > 0 || strings.Contains(slot.State, "prompt") {
			f.phase = "processing"
			f.promptN = slot.PromptN
			f.promptTotal = slot.PromptTotal
			break
		}
	}
}

func (s *Service) runStatus(ctx context.Context, f *flight) {
	s.broadcast(s.statusEvent(f))
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.pollSlot(ctx, f)
			s.broadcast(s.statusEvent(f))
		}
	}
}

func (s *Service) updateStats(promptTokens, completionTokens int, elapsed time.Duration) {
	updates := []struct {
		field string
		value int64
	}{
		{"queries", 1},
		{"promptTokens", int64(promptTokens)},
		{"completionTokens", int64(completionTokens)},
		{"genMs", elapsed.Milliseconds()},
	}
	for _, update := range updates {
		if err := s.valkey.hincrby(statsKey, update.field, update.value); err != nil {
			s.broadcast(marshalEvent(struct {
				Type string `json:"type"`
				Stats
			}{Type: "chat-stats", Stats: Stats{}}))
			return
		}
	}
	s.broadcast(s.StatsMessage())
}

func (s *Service) generate() {
	started := time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()
	statusCtx, stopStatus := context.WithCancel(context.Background())
	f := &flight{phase: "sending", started: started}
	go s.runStatus(statusCtx, f)
	defer func() {
		cancel()
		stopStatus()
		s.broadcast(marshalEvent(llmStatus{Type: "llm-status", Phase: "idle"}))
		s.clearBusy()
	}()

	if s.agent != nil {
		s.generateAgent(ctx, started)
		return
	}

	body := marshalEvent(map[string]any{"stream": true, "messages": s.contextMessages()})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.llamaURL, bytes.NewReader(body))
	if err != nil {
		s.broadcast(errorEvent("llama request failed: " + err.Error()))
		return
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			s.finishReply("", true, 0, 0, time.Since(started))
		} else {
			s.broadcast(errorEvent("llama request failed: " + err.Error()))
		}
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		s.broadcast(errorEvent("llama returned HTTP " + response.Status))
		return
	}

	var reply strings.Builder
	promptTokens, completionTokens := 0, 0
	generatingAnnounced := false
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	done := false
	for scanner.Scan() {
		data, found := strings.CutPrefix(scanner.Text(), "data: ")
		if !found {
			continue
		}
		if strings.TrimSpace(data) == "[DONE]" {
			done = true
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
			Timings struct {
				PromptN            int     `json:"prompt_n"`
				PredictedN         int     `json:"predicted_n"`
				PredictedPerSecond float64 `json:"predicted_per_second"`
			} `json:"timings"`
		}
		if json.Unmarshal([]byte(data), &chunk) != nil {
			continue
		}
		if chunk.Timings.PromptN > 0 {
			promptTokens = chunk.Timings.PromptN
		}
		if chunk.Timings.PredictedN > 0 {
			completionTokens = chunk.Timings.PredictedN
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta.Content
		if delta == "" {
			continue
		}
		reply.WriteString(delta)
		f.mu.Lock()
		f.phase = "generating"
		f.tokenTimes = append(f.tokenTimes, time.Now())
		f.mu.Unlock()
		if !generatingAnnounced {
			s.broadcast(s.statusEvent(f))
			generatingAnnounced = true
		}
		s.broadcast(marshalEvent(struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{Type: "chat-delta", Text: delta}))
	}
	stopped := ctx.Err() != nil
	if err := scanner.Err(); err != nil && !done && !stopped {
		s.broadcast(errorEvent("llama stream failed: " + err.Error()))
		return
	}
	f.mu.Lock()
	fallbackTokens := len(f.tokenTimes)
	f.mu.Unlock()
	if completionTokens == 0 {
		completionTokens = fallbackTokens
	}
	s.finishReply(reply.String(), stopped, promptTokens, completionTokens, time.Since(started))
}

func (s *Service) generateAgent(ctx context.Context, started time.Time) {
	s.mu.Lock()
	userText := ""
	if len(s.history) > 0 {
		userText = s.history[len(s.history)-1].Text
	}
	s.mu.Unlock()
	result, err := s.agent.Handle(ctx, userText)
	if err != nil {
		s.broadcast(errorEvent("agent failed: " + err.Error()))
		if result.Reply == "" {
			return
		}
	}
	s.finishReply(result.Reply, result.Stopped, 0, 0, time.Since(started))
}

func (s *Service) finishReply(text string, stopped bool, promptTokens, completionTokens int, elapsed time.Duration) {
	assistantMessage := Message{Role: "assistant", Text: text, Ts: time.Now().UnixMilli()}
	s.append(assistantMessage)
	s.clearBusy()
	s.broadcast(marshalEvent(struct {
		Type    string `json:"type"`
		Stopped bool   `json:"stopped,omitempty"`
		Message
	}{Type: "chat-done", Stopped: stopped, Message: assistantMessage}))
	s.updateStats(promptTokens, completionTokens, elapsed)
}
