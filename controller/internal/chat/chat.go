// Package chat implements the shared conversation: one history for the whole
// instance, streamed from llama-server and persisted in Valkey.
package chat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mayanklahiri/virtualme/controller/internal/agent"
	"github.com/mayanklahiri/virtualme/controller/internal/jobs"
	"github.com/mayanklahiri/virtualme/controller/internal/metrics"
	"github.com/mayanklahiri/virtualme/controller/internal/origin"
	"github.com/mayanklahiri/virtualme/controller/internal/valkey"
	"github.com/mayanklahiri/virtualme/controller/internal/ws"
	"github.com/mayanklahiri/virtualme/controller/prompts"
)

const (
	historyKey       = "virtualme:chat"
	statsKey         = "virtualme:chat-stats"
	historyCap       = 200
	contextWindow    = 16
	historyPromptCap = 16 * 1024
	maxTextLen       = 4096
)

// Message is one chat history entry.
type Message struct {
	ID            string  `json:"id,omitempty"`
	Role          string  `json:"role"`
	Text          string  `json:"text"`
	Ts            int64   `json:"ts"`
	CorrelationID string  `json:"correlationId,omitempty"`
	Source        *Source `json:"source,omitempty"`
}

type Source = origin.Source

type Submission struct {
	Text          string
	InitiatorID   string
	CorrelationID string
	Source        Source
}

type SubmitResult struct {
	MessageID     string
	CorrelationID string
	JobID         string
	Duplicate     bool
	Ahead         int
}

type Delivery struct {
	CorrelationID string
	JobID         string
	Source        origin.Source
	Text          string
	Err           error
	Stopped       bool
}

type DeliveryHandler func(Delivery) error

// Service owns the shared conversation state.
type Service struct {
	valkey     *valkey.Client
	llamaURL   string
	slotsURL   string
	broadcast  func([]byte)
	client     *http.Client
	retryDelay time.Duration
	agent      *agent.Agent
	jobs       *jobs.Manager
	activity   jobs.ActivityRecorder
	counters   *metrics.Counters

	mu             sync.Mutex
	history        []Message
	fallbackCancel context.CancelFunc
	deliveries     map[string]DeliveryHandler
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
		valkey:     valkey.New(valkeyAddr),
		llamaURL:   llamaURL,
		slotsURL:   slotsURL,
		broadcast:  broadcast,
		client:     &http.Client{Timeout: 120 * time.Second},
		retryDelay: 2 * time.Second,
		deliveries: make(map[string]DeliveryHandler),
	}
}

func (s *Service) RegisterDelivery(channel string, handler DeliveryHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.deliveries[channel]; exists {
		panic("chat: duplicate delivery channel " + channel)
	}
	s.deliveries[channel] = handler
}

func (s *Service) SubmitUserText(ctx context.Context, in Submission) (SubmitResult, error) {
	text := strings.TrimSpace(in.Text)
	if text == "" || len([]rune(text)) > maxTextLen || in.InitiatorID == "" || in.CorrelationID == "" ||
		(in.Source.Channel != "web" && in.Source.Channel != "telegram") {
		return SubmitResult{}, errors.New("invalid chat submission")
	}
	if in.Source.Channel == "telegram" && (in.Source.ChatID == "" || in.Source.UserID == "" || in.Source.UpdateID <= 0) {
		return SubmitResult{}, errors.New("invalid Telegram source")
	}
	messageID, jobID := jobs.NewID(), jobs.NewID()
	if in.Source.Channel == "telegram" {
		messageID = fmt.Sprintf("telegram-user:%d", in.Source.UpdateID)
		jobID = fmt.Sprintf("telegram-chat:%d", in.Source.UpdateID)
	}
	source := in.Source
	message := Message{ID: messageID, Role: "user", Text: text, Ts: time.Now().UnixMilli(), CorrelationID: in.CorrelationID, Source: &source}
	s.append(message)
	s.broadcast(marshalEvent(struct {
		Type string `json:"type"`
		Message
	}{Type: "chat-message", Message: message}))
	if _, err := s.valkey.HIncrBy(statsKey, "queries", 1); err == nil {
		s.broadcast(s.StatsMessage())
	}
	body, _ := json.Marshal(map[string]string{"text": text})
	kind := in.Source.Channel
	initiator := jobs.Initiator{ID: in.InitiatorID, Kind: kind, CancelOnDisconnect: false}
	if kind == "web" {
		initiator.ConnectionID = strings.TrimPrefix(in.InitiatorID, "ws:")
		initiator.CancelOnDisconnect = true
	}
	env := jobs.Envelope{
		ID: jobID, Type: "chat", Payload: body, Priority: "interactive", EnqueuedTs: message.Ts,
		Initiator: initiator, CorrelationID: in.CorrelationID, Source: &source, VisibilityTimeoutSec: 900,
	}
	if s.jobs == nil {
		runCtx, cancel := context.WithCancel(ctx)
		s.mu.Lock()
		s.fallbackCancel = cancel
		s.mu.Unlock()
		go func() {
			_, _ = s.generate(runCtx, env)
			s.mu.Lock()
			s.fallbackCancel = nil
			s.mu.Unlock()
			cancel()
		}()
		return SubmitResult{MessageID: messageID, CorrelationID: in.CorrelationID, JobID: jobID}, nil
	}
	ahead, err := s.jobs.Enqueue(env)
	if err != nil {
		return SubmitResult{}, err
	}
	return SubmitResult{MessageID: messageID, CorrelationID: in.CorrelationID, JobID: jobID, Ahead: ahead}, nil
}

// SetJobManager routes interactive generation through manager.
func (s *Service) SetJobManager(manager *jobs.Manager) {
	s.jobs = manager
}

// SetActivity installs the machine activity recorder.
func (s *Service) SetActivity(activity jobs.ActivityRecorder) {
	s.activity = activity
}

// SetCounters installs the metrics accumulator for fallback-chat LLM usage.
func (s *Service) SetCounters(counters *metrics.Counters) {
	s.counters = counters
}

// NewWithAgent creates the production chat service with browser-agent routing.
func NewWithAgent(valkeyAddr, llamaURL string, broadcast func([]byte), agentConfig agent.Config) *Service {
	service := New(valkeyAddr, llamaURL, broadcast)
	agentConfig.LlamaURL = llamaURL
	agentConfig.Broadcast = broadcast
	agentConfig.History = func() []agent.PromptMessage {
		service.mu.Lock()
		defer service.mu.Unlock()
		recent := boundedHistory(service.history)
		messages := make([]agent.PromptMessage, 0, len(recent))
		for _, message := range recent {
			messages = append(messages, agent.PromptMessage{Role: message.Role, Content: message.Text})
		}
		return messages
	}
	service.agent = agent.New(agentConfig)
	return service
}

func boundedHistory(history []Message) []Message {
	start := max(0, len(history)-contextWindow)
	size := 0
	for index := len(history) - 1; index >= start; index-- {
		next := len(history[index].Text)
		if size > 0 && size+next > historyPromptCap {
			start = index + 1
			break
		}
		size += next
	}
	return history[start:]
}

// LoadHistory populates memory from Valkey, retrying until Valkey responds so
// a controller that boots before Valkey still recovers the conversation.
// Run it in a goroutine; a permanently-down Valkey only costs a warning log.
func (s *Service) LoadHistory() {
	for attempt := 0; ; attempt++ {
		entries, err := s.valkey.LRange(historyKey, 0, -1)
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

// AgentReplayFrames returns buffered agent-step frames for the latest task,
// or nil when no agent is configured.
func (s *Service) AgentReplayFrames() [][]byte {
	if s.agent == nil {
		return nil
	}
	return s.agent.ReplayFrames()
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
	values, err := s.valkey.HGetAll(statsKey)
	if err != nil {
		return Stats{}
	}
	return Stats{
		Queries: parseStat(values["queries"]), PromptTokens: parseStat(values["promptTokens"]),
		CompletionTokens: parseStat(values["completionTokens"]), GenMS: parseStat(values["genMs"]),
	}
}

func parseStat(value string) int64 {
	number, _ := strconv.ParseInt(value, 10, 64)
	return number
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
	if _, err := s.valkey.RPush(historyKey, string(encoded)); err != nil {
		log.Println("chat: persist failed:", err)
		return
	}
	if err := s.valkey.LTrim(historyKey, -historyCap, -1); err != nil {
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
		if s.jobs != nil {
			connID := ""
			if c != nil {
				connID = c.ID()
			}
			s.jobs.CancelRunningConnection(connID, "chat", "stopped")
			s.jobs.DropQueued(connID, "chat")
		} else {
			s.mu.Lock()
			cancel := s.fallbackCancel
			s.mu.Unlock()
			if cancel != nil {
				cancel()
			}
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
	if text == "" || len([]rune(text)) > maxTextLen {
		writeError(c, "message must be 1-4096 characters")
		return
	}
	connID := ""
	if c != nil {
		connID = c.ID()
	}
	correlationID := jobs.NewID()
	result, err := s.SubmitUserText(context.Background(), Submission{
		Text: text, InitiatorID: "ws:" + connID, CorrelationID: correlationID,
		Source: Source{Channel: "web"},
	})
	if err != nil {
		writeError(c, "job enqueue failed")
		return
	}
	if result.Ahead > 0 {
		s.broadcast(marshalEvent(struct {
			Type   string `json:"type"`
			Phase  string `json:"phase"`
			Detail string `json:"detail"`
		}{Type: "llm-status", Phase: "queued", Detail: fmt.Sprintf("queued behind %d jobs", result.Ahead)}))
	}
}

func (s *Service) clear(c *ws.Conn) {
	s.mu.Lock()
	s.history = nil
	s.mu.Unlock()
	if _, err := s.valkey.Del(historyKey, statsKey); err != nil {
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

// contextMessages returns the OpenAI-style message list for the completion.
func (s *Service) contextMessages(throughTs int64) []map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	end := len(s.history)
	if throughTs > 0 {
		for index, message := range s.history {
			if message.Role == "user" && message.Ts == throughTs {
				end = index + 1
				break
			}
		}
	}
	recent := boundedHistory(s.history[:end])
	messages := make([]map[string]string, 0, len(recent)+1)
	messages = append(messages, map[string]string{"role": "system", "content": prompts.Chat})
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
		{"promptTokens", int64(promptTokens)},
		{"completionTokens", int64(completionTokens)},
		{"genMs", elapsed.Milliseconds()},
	}
	for _, update := range updates {
		if _, err := s.valkey.HIncrBy(statsKey, update.field, update.value); err != nil {
			s.broadcast(marshalEvent(struct {
				Type string `json:"type"`
				Stats
			}{Type: "chat-stats", Stats: Stats{}}))
			return
		}
	}
	s.broadcast(s.StatsMessage())
}

// Execute runs one queued chat envelope.
func (s *Service) Execute(ctx context.Context, env jobs.Envelope) (string, error) {
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(env.Payload, &payload); err != nil || strings.TrimSpace(payload.Text) == "" {
		return "", fmt.Errorf("invalid chat payload")
	}
	return s.generate(ctx, env)
}

// RunTask runs an isolated agent turn without reading or writing shared chat history.
func (s *Service) RunTask(ctx context.Context, text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" || len([]rune(text)) > 8192 {
		return "", fmt.Errorf("task must be 1-8192 characters")
	}
	if s.agent == nil {
		return "", fmt.Errorf("agent is unavailable")
	}
	started := time.Now()
	s.recordLLMStart(ctx, text)
	result, err := s.agent.HandleFresh(ctx, text)
	s.recordLLMFinish(ctx, result.PromptTokens, result.CompletionTokens, time.Since(started), result.Stopped, err == nil && !result.Failed)
	if err != nil && result.Reply == "" {
		return "", err
	}
	if ctx.Err() != nil {
		return result.Reply, ctx.Err()
	}
	if err != nil {
		return result.Reply, err
	}
	if result.Failed {
		return result.Reply, fmt.Errorf("agent task failed")
	}
	if result.Stopped {
		return result.Reply, fmt.Errorf("agent task stopped")
	}
	return result.Reply, err
}

func (s *Service) generate(ctx context.Context, env jobs.Envelope) (string, error) {
	started := time.Now()
	var prompt struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(env.Payload, &prompt)
	s.recordLLMStart(ctx, prompt.Text)
	llmFinished := false
	statusCtx, stopStatus := context.WithCancel(context.Background())
	f := &flight{phase: "sending", started: started}
	go s.runStatus(statusCtx, f)
	defer func() {
		stopStatus()
		s.broadcast(marshalEvent(llmStatus{Type: "llm-status", Phase: "idle"}))
		if !llmFinished {
			s.recordLLMFinish(ctx, 0, 0, time.Since(started), ctx.Err() != nil, false)
		}
	}()

	if s.agent != nil {
		summary, err := s.generateAgent(ctx, started, prompt.Text, env)
		llmFinished = true
		return summary, err
	}

	body := marshalEvent(map[string]any{"stream": true, "messages": s.contextMessages(env.EnqueuedTs)})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.llamaURL, bytes.NewReader(body))
	if err != nil {
		s.broadcast(errorEvent("llama request failed: " + err.Error()))
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			s.finishReply(ctx, env, "", true, 0, 0, time.Since(started))
			llmFinished = true
			return "stopped", nil
		} else {
			s.broadcast(errorEvent("llama request failed: " + err.Error()))
		}
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		s.broadcast(errorEvent("llama returned HTTP " + response.Status))
		return "", fmt.Errorf("llama returned HTTP %s", response.Status)
	}

	var reply strings.Builder
	promptTokens, completionTokens := 0, 0
	cachedTokens := 0
	promptMs, predictedMs := 0.0, 0.0
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
				CacheN             int     `json:"cache_n"`
				PromptMs           float64 `json:"prompt_ms"`
				PredictedMs        float64 `json:"predicted_ms"`
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
		return "", err
	}
	f.mu.Lock()
	fallbackTokens := len(f.tokenTimes)
	f.mu.Unlock()
	if completionTokens == 0 {
		completionTokens = fallbackTokens
	}
	if s.counters != nil {
		s.counters.AddLLM(promptTokens, completionTokens, cachedTokens, promptMs, predictedMs)
	}
	s.finishReply(ctx, env, reply.String(), stopped, promptTokens, completionTokens, time.Since(started))
	llmFinished = true
	return "chat reply completed", nil
}

func (s *Service) generateAgent(ctx context.Context, started time.Time, userText string, env jobs.Envelope) (string, error) {
	result, err := s.agent.Handle(ctx, userText)
	if err != nil {
		s.broadcast(errorEvent("agent failed: " + err.Error()))
		if result.Reply == "" {
			s.recordLLMFinish(ctx, result.PromptTokens, result.CompletionTokens, time.Since(started), result.Stopped, false)
			return "", err
		}
	}
	s.finishReply(
		ctx,
		env,
		result.Reply,
		result.Stopped,
		result.PromptTokens,
		result.CompletionTokens,
		time.Since(started),
	)
	return "chat reply completed", nil
}

func (s *Service) finishReply(ctx context.Context, env jobs.Envelope, text string, stopped bool, promptTokens, completionTokens int, elapsed time.Duration) {
	var source *Source
	if env.Source != nil {
		copy := Source(*env.Source)
		source = &copy
	}
	assistantMessage := Message{
		ID: jobs.NewID(), Role: "assistant", Text: text, Ts: time.Now().UnixMilli(),
		CorrelationID: env.CorrelationID, Source: source,
	}
	s.append(assistantMessage)
	s.broadcast(marshalEvent(struct {
		Type    string `json:"type"`
		Stopped bool   `json:"stopped,omitempty"`
		Message
	}{Type: "chat-done", Stopped: stopped, Message: assistantMessage}))
	s.updateStats(promptTokens, completionTokens, elapsed)
	s.recordLLMFinish(ctx, promptTokens, completionTokens, elapsed, stopped, !stopped)
	if source != nil {
		s.mu.Lock()
		handler := s.deliveries[source.Channel]
		s.mu.Unlock()
		if handler != nil {
			if err := handler(Delivery{
				CorrelationID: env.CorrelationID, JobID: env.ID, Source: *source,
				Text: text, Stopped: stopped,
			}); err != nil {
				log.Printf("chat: %s delivery failed", source.Channel)
			}
		}
	}
}

func (s *Service) recordLLMStart(ctx context.Context, prompt string) {
	if s.activity == nil {
		return
	}
	excerpt := truncateRunes(strings.TrimSpace(prompt), 120)
	_ = s.activity.Record(jobs.ActivityEvent{
		Kind: "llm", Name: "generate", JobID: jobs.JobID(ctx), Summary: "prompt: " + excerpt,
		Detail: jobs.ActivityDetail{Phase: "start", OK: true},
	})
}

func (s *Service) recordLLMFinish(ctx context.Context, promptTokens, completionTokens int, elapsed time.Duration, stopped, ok bool) {
	if s.activity == nil {
		return
	}
	summary := "Generation completed"
	if stopped {
		summary = "Generation stopped"
	} else if !ok {
		summary = "Generation failed"
	}
	_ = s.activity.Record(jobs.ActivityEvent{
		Kind: "llm", Name: "generate", JobID: jobs.JobID(ctx), Summary: summary,
		Detail: jobs.ActivityDetail{
			Phase: "finish", DurationMS: elapsed.Milliseconds(), OK: ok, Stopped: stopped,
			PromptTokens: promptTokens, CompletionTokens: completionTokens,
		},
	})
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
