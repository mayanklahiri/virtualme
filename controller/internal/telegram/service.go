package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/mayanklahiri/virtualme/controller/internal/jobs"
	"github.com/mayanklahiri/virtualme/controller/internal/notifications"
	"github.com/mayanklahiri/virtualme/controller/internal/valkey"
	"github.com/mayanklahiri/virtualme/controller/internal/ws"
)

const (
	offsetKey       = "virtualme:telegram:update-offset"
	eventsKey       = "virtualme:telegram:events"
	chatsKey        = "virtualme:telegram:known-chats"
	ingressIndexKey = "virtualme:chat:ingress:telegram:index"
)

const reserveIngressScript = `
if redis.call('EXISTS', KEYS[1]) == 1 then return redis.call('GET', KEYS[1]) end
redis.call('SET', KEYS[1], ARGV[1])
redis.call('RPUSH', KEYS[2], ARGV[2])
return ARGV[1]`

type Clock interface {
	Now() time.Time
	Sleep(context.Context, time.Duration) error
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
func (realClock) Sleep(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type Jitter func() float64

type Service struct {
	mu        sync.Mutex
	config    Config
	api       API
	store     *valkey.Client
	broadcast func([]byte)
	notify    *notifications.Service
	clock     Clock
	jitter    Jitter
	status    Status
	known     map[string]Destination
	inflight  map[string]struct{}
	submit    func(context.Context, int64, string, string, string) error
	jobState  func() string
}

func New(config Config, api API, store *valkey.Client, broadcast func([]byte), notify *notifications.Service) *Service {
	if broadcast == nil {
		broadcast = func([]byte) {}
	}
	state := "disabled"
	detail := "Telegram is disabled"
	if config.Enabled {
		state, detail = "connecting", "Connecting to Telegram"
	}
	return &Service{
		config: config, api: api, store: store, broadcast: broadcast, notify: notify,
		clock: realClock{}, jitter: func() float64 { return 0.5 }, known: map[string]Destination{},
		inflight: map[string]struct{}{}, status: Status{
			Enabled: config.Enabled, State: state, Detail: detail,
			Poll:        PollStatus{TimeoutSeconds: config.PollTimeoutSeconds},
			MaxEventLog: config.MaxEventLog,
		},
	}
}

func (s *Service) SetSubmitter(fn func(context.Context, int64, string, string, string) error) {
	s.submit = fn
}

func (s *Service) SetJobState(fn func() string) { s.jobState = fn }

func Authorized(config Config, chatID, userID string, isBot bool) bool {
	if isBot || chatID == "" || userID == "" || !contains(config.AllowedChatIDs, chatID) {
		return false
	}
	return len(config.AllowedUserIDs) == 0 || contains(config.AllowedUserIDs, userID)
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func NewEvent(update Update, outcome, activeToken string) Event {
	event := Event{
		ID: jobs.NewID(), Ts: time.Now().UnixMilli(), UpdateID: update.UpdateID,
		Kind: "message", Outcome: outcome, Detail: eventDetail(outcome), RawUpdate: json.RawMessage(`{}`),
	}
	message := update.Message
	if update.EditedMessage != nil {
		message, event.Kind = update.EditedMessage, "edited_message"
	}
	if message != nil {
		if message.Chat != nil {
			event.ChatID = strconv.FormatInt(message.Chat.ID, 10)
			event.ChatType = sanitize(message.Chat.Type, 128)
			event.ChatLabel = chatLabel(*message.Chat)
		}
		if message.From != nil {
			event.UserID = strconv.FormatInt(message.From.ID, 10)
			event.Username = sanitize(message.From.Username, 128)
		}
		event.MessageID = message.MessageID
		if outcome == "accepted" || outcome == "rejected_too_long" {
			event.TextPreview = preview(message.Text, 160)
		}
	}
	raw := bytes.TrimSpace(update.Raw)
	var object map[string]any
	valid := len(raw) <= 16384 && json.Unmarshal(raw, &object) == nil && object != nil &&
		(activeToken == "" || !recursiveContains(object, activeToken))
	if valid {
		compacted, _ := json.Marshal(object)
		if len(compacted) <= 16384 {
			event.RawUpdate = compacted
		} else {
			event.RawOmitted = true
		}
	} else if len(raw) > 0 {
		event.RawOmitted = true
	}
	return event
}

func recursiveContains(value any, sentinel string) bool {
	switch typed := value.(type) {
	case string:
		return strings.Contains(typed, sentinel)
	case []any:
		for _, item := range typed {
			if recursiveContains(item, sentinel) {
				return true
			}
		}
	case map[string]any:
		for key, item := range typed {
			if strings.Contains(key, sentinel) || recursiveContains(item, sentinel) {
				return true
			}
		}
	}
	return false
}

func sanitize(value string, capRunes int) string {
	out := []rune(value)
	for index, char := range out {
		if unicode.IsControl(char) && char != '\n' && char != '\t' {
			out[index] = '\uFFFD'
		}
	}
	if len(out) > capRunes {
		out = out[:capRunes]
	}
	return string(out)
}

func preview(value string, limit int) string {
	value = strings.Join(strings.Fields(sanitize(value, limit+1)), " ")
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return value
}

func chatLabel(chat Chat) string {
	switch {
	case chat.Title != "":
		return sanitize(chat.Title, 128)
	case chat.Username != "":
		return sanitize("@"+chat.Username, 128)
	case strings.TrimSpace(chat.FirstName+" "+chat.LastName) != "":
		return sanitize(strings.TrimSpace(chat.FirstName+" "+chat.LastName), 128)
	default:
		return "Chat " + strconv.FormatInt(chat.ID, 10)
	}
}

func eventDetail(outcome string) string {
	table := map[string]string{
		"accepted": "Message accepted", "denied": "Update was not authorized",
		"ignored_edit": "Edited messages are ignored", "ignored_bot": "Bot-authored messages are ignored",
		"ignored_non_text": "Non-text messages are ignored", "ignored_malformed": "Malformed update ignored",
		"ignored_unsupported": "Unsupported update ignored", "command": "Command handled",
		"unknown_command": "Unknown command handled", "rejected_too_long": "Message exceeded 4096 characters",
	}
	return table[outcome]
}

func (s *Service) StatusMessage() []byte {
	s.mu.Lock()
	status := s.status
	status.Destinations = s.destinationsLocked()
	s.mu.Unlock()
	body, _ := json.Marshal(map[string]any{"type": "telegram-status", "status": status})
	return body
}

func (s *Service) destinationsLocked() []Destination {
	result := make([]Destination, 0, len(s.config.AllowedChatIDs))
	for _, id := range s.config.AllowedChatIDs {
		if observed, ok := s.known[id]; ok {
			result = append(result, observed)
		} else {
			result = append(result, Destination{ChatID: id, Label: "Chat " + id})
		}
	}
	return result
}

func (s *Service) EventsMessage() []byte {
	events := s.loadEvents(50)
	body, _ := json.Marshal(map[string]any{"type": "telegram-events", "events": summaries(events), "eventCount": s.eventCount()})
	return body
}

func summaries(events []Event) []map[string]any {
	result := make([]map[string]any, 0, len(events))
	for index := len(events) - 1; index >= 0; index-- {
		raw, _ := json.Marshal(events[index])
		var item map[string]any
		_ = json.Unmarshal(raw, &item)
		delete(item, "rawUpdate")
		result = append(result, item)
	}
	return result
}

func (s *Service) eventCount() int {
	if s.store == nil {
		return 0
	}
	count, _ := s.store.LLen(eventsKey)
	return int(count)
}

func (s *Service) loadEvents(limit int) []Event {
	if s.store == nil {
		return nil
	}
	rows, _ := s.store.LRange(eventsKey, -limit, -1)
	result := make([]Event, 0, len(rows))
	for _, row := range rows {
		var event Event
		if json.Unmarshal([]byte(row), &event) == nil {
			result = append(result, event)
		}
	}
	return result
}

func (s *Service) appendEvent(event Event) error {
	if s.store == nil {
		return errors.New("storage unavailable")
	}
	encoded, _ := json.Marshal(event)
	if _, err := s.store.RPush(eventsKey, string(encoded)); err != nil {
		return err
	}
	if err := s.store.LTrim(eventsKey, -s.config.MaxEventLog, -1); err != nil {
		return err
	}
	s.mu.Lock()
	s.status.EventCount = s.eventCount()
	s.mu.Unlock()
	frame, _ := json.Marshal(map[string]any{"type": "telegram-event", "event": event})
	s.broadcast(frame)
	return nil
}

func (s *Service) Start(ctx context.Context) {
	if !s.config.Enabled || s.api == nil {
		return
	}
	go s.poll(ctx)
}

func (s *Service) poll(ctx context.Context) {
	me, err := s.api.GetMe(ctx)
	if err != nil || !me.IsBot {
		code := errorCode(err)
		if err == nil {
			code = "identity_not_bot"
		}
		s.setState("auth_failed", code)
		s.notifyLifecycle(ctx, "error", "Telegram authentication failed", "Update the Telegram bot-token secret in Config.")
		return
	}
	s.mu.Lock()
	s.status.Bot = BotIdentity{ID: strconv.FormatInt(me.ID, 10), Username: me.Username, DisplayName: strings.TrimSpace(me.FirstName + " " + me.LastName)}
	s.mu.Unlock()
	s.setState("connected", "")
	s.notifyLifecycle(ctx, "info", "Telegram connected", "The configured bot is authenticated and long polling is active.")
	offset := int64(0)
	if s.store != nil {
		if raw, getErr := s.store.Get(offsetKey); getErr == nil && raw != nil {
			offset, _ = strconv.ParseInt(*raw, 10, 64)
		}
	}
	failures := 0
	for ctx.Err() == nil {
		updates, pollErr := s.api.GetUpdates(ctx, GetUpdatesRequest{
			Offset: offset, Timeout: s.config.PollTimeoutSeconds,
			AllowedUpdates: []string{"message", "edited_message"},
		})
		if pollErr != nil {
			if ctx.Err() != nil {
				break
			}
			failures++
			code := errorCode(pollErr)
			if code == "authentication_failed" {
				s.setState("auth_failed", code)
				return
			}
			state := "backing_off"
			if code == "poll_conflict" || failures >= 5 {
				state = "polling_suspended"
			}
			s.setState(state, code)
			delay := backoff(failures, s.jitter())
			var apiErr *APIError
			if errors.As(pollErr, &apiErr) && apiErr.RetryAfter > 0 {
				delay = min(max(apiErr.RetryAfter, time.Second), 15*time.Minute)
			}
			if s.clock.Sleep(ctx, delay) != nil {
				break
			}
			continue
		}
		failures = 0
		s.setState("connected", "")
		sort.Slice(updates, func(i, j int) bool { return updates[i].UpdateID < updates[j].UpdateID })
		for _, update := range updates {
			if update.UpdateID < offset {
				continue
			}
			if err := s.process(ctx, update); err != nil {
				s.setState("backing_off", "storage_unavailable")
				break
			}
			offset = update.UpdateID + 1
			if s.store == nil || s.store.Set(offsetKey, strconv.FormatInt(offset, 10)) != nil {
				s.setState("backing_off", "storage_unavailable")
				break
			}
			s.mu.Lock()
			s.status.Poll.NextOffset = offset
			s.status.Poll.LastSuccessTs = s.clock.Now().UnixMilli()
			s.mu.Unlock()
		}
	}
	s.setState("stopped", "")
}

func (s *Service) process(ctx context.Context, update Update) error {
	outcome := "ignored_unsupported"
	message := update.Message
	switch {
	case update.EditedMessage != nil:
		outcome = "ignored_edit"
	case message == nil:
		outcome = "ignored_unsupported"
	case message.From != nil && message.From.IsBot:
		outcome = "ignored_bot"
	case message.From == nil || message.Chat == nil:
		outcome = "ignored_malformed"
	default:
		chatID, userID := strconv.FormatInt(message.Chat.ID, 10), strconv.FormatInt(message.From.ID, 10)
		if !Authorized(s.config, chatID, userID, message.From.IsBot) {
			outcome = "denied"
		} else {
			s.observe(*message.Chat)
			text := strings.TrimSpace(message.Text)
			switch {
			case text == "":
				outcome = "ignored_non_text"
			case commandName(*message, s.status.Bot.Username) != "":
				outcome = s.handleCommand(ctx, chatID, commandName(*message, s.status.Bot.Username))
			case len([]rune(text)) > 4096:
				outcome = "rejected_too_long"
				_ = s.send(ctx, chatID, "That message is too long. Please keep it to 4096 characters.")
			default:
				outcome = "accepted"
				if s.submit != nil {
					_ = s.api.SendChatAction(ctx, SendChatActionRequest{ChatID: chatID, Action: "typing"})
					key := fmt.Sprintf("virtualme:chat:ingress:telegram:%d", update.UpdateID)
					stage := "reserved"
					if s.store != nil {
						reply, err := s.store.Eval(reserveIngressScript, []string{key, ingressIndexKey}, stage, strconv.FormatInt(update.UpdateID, 10))
						if err != nil {
							return err
						}
						if current, ok := reply.(string); ok {
							stage = current
						}
					}
					if stage != "complete" {
						if err := s.submit(ctx, update.UpdateID, chatID, userID, text); err != nil {
							return err
						}
						if s.store == nil || s.store.Set(key, "complete") != nil {
							return errors.New("storage unavailable")
						}
					}
				}
			}
		}
	}
	return s.appendEvent(NewEvent(update, outcome, s.config.BotToken))
}

func (s *Service) observe(chat Chat) {
	destination := Destination{ChatID: strconv.FormatInt(chat.ID, 10), Label: chatLabel(chat), Type: sanitize(chat.Type, 128), Observed: true}
	s.mu.Lock()
	s.known[destination.ChatID] = destination
	s.mu.Unlock()
	if s.store != nil {
		encoded, _ := json.Marshal(destination)
		_, _ = s.store.RPush(chatsKey, string(encoded))
		_ = s.store.LTrim(chatsKey, -200, -1)
	}
}

func commandName(message Message, botUsername string) string {
	if len(message.Entities) == 0 || message.Entities[0].Type != "bot_command" || message.Entities[0].Offset != 0 {
		return ""
	}
	runes := []rune(message.Text)
	if message.Entities[0].Length <= 0 || message.Entities[0].Length > len(runes) {
		return "unknown"
	}
	value := strings.ToLower(string(runes[:message.Entities[0].Length]))
	parts := strings.SplitN(value, "@", 2)
	if len(parts) == 2 && !strings.EqualFold(parts[1], botUsername) {
		return "unknown"
	}
	if len(strings.Fields(strings.TrimSpace(message.Text))) != 1 {
		return "unknown"
	}
	switch parts[0] {
	case "/start", "/help":
		return "help"
	case "/status":
		return "status"
	default:
		return "unknown"
	}
}

func (s *Service) handleCommand(ctx context.Context, chatID, command string) string {
	text := "Unknown command. Commands: /help, /status."
	outcome := "unknown_command"
	switch command {
	case "help":
		text, outcome = "Virtual Me shares one conversation with the web console. Send text to ask a question. Commands: /help, /status.", "command"
	case "status":
		state := "idle"
		if s.jobState != nil {
			state = s.jobState()
		}
		text, outcome = fmt.Sprintf("Virtual Me is %s. Telegram is connected as @%s.", state, s.status.Bot.Username), "command"
	}
	_ = s.send(ctx, chatID, text)
	return outcome
}

func (s *Service) send(ctx context.Context, chatID, text string) error {
	for _, chunk := range ChunkText(text) {
		if _, err := s.api.SendMessage(ctx, SendMessageRequest{ChatID: chatID, Text: chunk}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) Deliver(ctx context.Context, chatID, text string) error {
	return s.send(ctx, chatID, text)
}

func (s *Service) HandleMessage(conn *ws.Conn, payload []byte) bool {
	var envelope struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(payload, &envelope) != nil || !strings.HasPrefix(envelope.Type, "telegram-") {
		return false
	}
	switch envelope.Type {
	case "telegram-status-req":
		_ = conn.WriteText(s.StatusMessage())
	case "telegram-events-req":
		_ = conn.WriteText(s.EventsMessage())
	case "telegram-event-detail-req":
		s.handleDetail(conn, payload)
	case "telegram-test-send":
		s.handleTestSend(conn, payload)
	default:
		return false
	}
	return true
}

func decodeStrict(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON")
	}
	return nil
}

func (s *Service) handleDetail(conn *ws.Conn, payload []byte) {
	var request struct {
		Type, RequestID, ID string
	}
	_ = json.Unmarshal(payload, &request)
	for _, event := range s.loadEvents(s.config.MaxEventLog) {
		if event.ID == request.ID {
			frame, _ := json.Marshal(map[string]any{"type": "telegram-event-detail", "requestId": request.RequestID, "event": event, "error": ""})
			_ = conn.WriteText(frame)
			return
		}
	}
	frame, _ := json.Marshal(map[string]any{"type": "telegram-event-detail", "requestId": request.RequestID, "event": nil, "error": "Telegram event is no longer retained"})
	_ = conn.WriteText(frame)
}

func (s *Service) handleTestSend(conn *ws.Conn, payload []byte) {
	var request struct {
		Type, ID, ChatID, Text string
	}
	errorText := ""
	if decodeStrict(payload, &request) != nil || strings.TrimSpace(request.Text) == "" || len([]rune(request.Text)) > 4096 {
		errorText = "Message must be 1–4096 characters"
	} else if !contains(s.config.AllowedChatIDs, request.ChatID) {
		errorText = "Destination is not authorized"
	} else {
		s.mu.Lock()
		connected := s.status.State == "connected"
		_, duplicate := s.inflight[request.ID]
		if connected && !duplicate {
			s.inflight[request.ID] = struct{}{}
		}
		s.mu.Unlock()
		switch {
		case duplicate:
			errorText = "Request is already running"
		case !connected:
			errorText = "Telegram is not connected"
		default:
			if err := s.send(context.Background(), request.ChatID, request.Text); err != nil {
				errorText = "Telegram could not send the test message"
			}
			s.mu.Lock()
			delete(s.inflight, request.ID)
			s.mu.Unlock()
		}
	}
	frame, _ := json.Marshal(map[string]any{"type": "telegram-command-result", "id": request.ID, "ok": errorText == "", "error": errorText})
	_ = conn.WriteText(frame)
}

func (s *Service) setState(state, code string) {
	details := map[string]string{
		"disabled": "Telegram is disabled", "connecting": "Connecting to Telegram",
		"connected": "Polling for authorized messages", "backing_off": "Retrying Telegram connection",
		"polling_suspended": "Telegram polling is suspended; retries continue",
		"auth_failed":       "Telegram authentication failed", "stopped": "Telegram service stopped",
	}
	s.mu.Lock()
	changed := s.status.State != state || s.status.Code != code
	s.status.State, s.status.Code, s.status.Detail = state, code, details[state]
	s.mu.Unlock()
	if changed {
		s.broadcast(s.StatusMessage())
	}
}

func errorCode(err error) string {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Code
	}
	return "transport_error"
}

func backoff(failures int, jitter float64) time.Duration {
	delay := time.Second << min(max(failures-1, 0), 6)
	if delay > 60*time.Second {
		delay = 60 * time.Second
	}
	delay = time.Duration(float64(delay) * (0.8 + 0.4*jitter))
	return min(max(delay, 800*time.Millisecond), 60*time.Second)
}

func (s *Service) notifyLifecycle(ctx context.Context, severity, title, summary string) {
	if s.notify == nil {
		return
	}
	_, err := s.notify.Create(ctx, notifications.CreateRequest{
		Type: severity, Sender: "telegram", Title: title, Summary: summary, Renderer: "generic",
	})
	if err != nil {
		log.Printf("telegram: notification: storage_unavailable")
	}
}
