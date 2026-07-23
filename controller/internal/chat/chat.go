// Package chat implements the shared conversation: one history for the whole
// instance, streamed from llama-server and persisted in Valkey.
package chat

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mayanklahiri/virtualme/controller/internal/ws"
)

const (
	historyKey    = "virtualme:chat"
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
	broadcast  func([]byte)
	client     *http.Client
	retryDelay time.Duration

	mu      sync.Mutex
	history []Message
	busy    bool
}

// New creates a chat service; broadcast sends a text frame to every client.
func New(valkeyAddr, llamaURL string, broadcast func([]byte)) *Service {
	return &Service{
		valkey:     newValkeyClient(valkeyAddr),
		llamaURL:   llamaURL,
		broadcast:  broadcast,
		client:     &http.Client{Timeout: 120 * time.Second},
		retryDelay: 2 * time.Second,
	}
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

// HandleClientMessage processes one client text frame.
func (s *Service) HandleClientMessage(c *ws.Conn, payload []byte) {
	var request struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(payload, &request) != nil || request.Type != "chat" {
		_ = c.WriteText(errorEvent("unrecognized message"))
		return
	}
	text := strings.TrimSpace(request.Text)
	if text == "" || len(text) > maxTextLen {
		_ = c.WriteText(errorEvent("message must be 1-4096 characters"))
		return
	}

	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		_ = c.WriteText(errorEvent("busy: a reply is already streaming"))
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

func (s *Service) clearBusy() {
	s.mu.Lock()
	s.busy = false
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

func (s *Service) generate() {
	defer s.clearBusy()

	body := marshalEvent(map[string]any{
		"stream":   true,
		"messages": s.contextMessages(),
	})
	response, err := s.client.Post(s.llamaURL, "application/json", bytes.NewReader(body))
	if err != nil {
		s.broadcast(errorEvent("llama request failed: " + err.Error()))
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		s.broadcast(errorEvent("llama returned HTTP " + response.Status))
		return
	}

	var reply strings.Builder
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
		}
		if json.Unmarshal([]byte(data), &chunk) != nil || len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta.Content
		if delta == "" {
			continue
		}
		reply.WriteString(delta)
		s.broadcast(marshalEvent(struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{Type: "chat-delta", Text: delta}))
	}
	if err := scanner.Err(); err != nil && !done {
		s.broadcast(errorEvent("llama stream failed: " + err.Error()))
		return
	}

	assistantMessage := Message{Role: "assistant", Text: reply.String(), Ts: time.Now().UnixMilli()}
	s.append(assistantMessage)
	s.broadcast(marshalEvent(struct {
		Type string `json:"type"`
		Message
	}{Type: "chat-done", Message: assistantMessage}))
}
