package telegram

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf16"

	"github.com/mayanklahiri/virtualme/controller/internal/config"
	"github.com/mayanklahiri/virtualme/controller/internal/jobs"
	"github.com/mayanklahiri/virtualme/controller/internal/notifications"
	"github.com/mayanklahiri/virtualme/controller/internal/ws"
)

const (
	offsetKey       = "virtualme:telegram:update-offset"
	eventsKey       = "virtualme:telegram:events"
	chatsKey        = "virtualme:telegram:known-chats"
	notificationKey = "virtualme:telegram:notification-state"
)

const appendEventScript = `
redis.call('RPUSH', KEYS[1], ARGV[1])
redis.call('LTRIM', KEYS[1], -tonumber(ARGV[2]), -1)
return redis.call('LLEN', KEYS[1])`

const upsertChatScript = `
local rows = redis.call('LRANGE', KEYS[1], 0, -1)
for _, row in ipairs(rows) do
  local ok, item = pcall(cjson.decode, row)
  if ok and item.chatId == ARGV[1] then redis.call('LREM', KEYS[1], 0, row) end
end
redis.call('RPUSH', KEYS[1], ARGV[2])
redis.call('LTRIM', KEYS[1], -200, -1)
return 1`

var (
	canonicalChatID = regexp.MustCompile(`^-?[1-9][0-9]*$`)
	requestID       = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)
)

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
type APIFactory func(string) (API, error)

type secretResolver interface {
	Resolve(string, time.Duration) ([]byte, config.SecretStatus, error)
	Subscribe(string, func(config.SecretRevision)) func()
}

type telegramStore interface {
	Get(string) (*string, error)
	Set(string, string) error
	LRange(string, int, int) ([]string, error)
	LLen(string) (int64, error)
	Eval(string, []string, ...string) (any, error)
	HGetAll(string) (map[string]string, error)
	HSet(string, ...string) (int64, error)
	HDel(string, ...string) (int64, error)
}

type KnownChat struct {
	ChatID     string `json:"chatId"`
	Type       string `json:"type"`
	Label      string `json:"label"`
	Username   string `json:"username"`
	LastSeenTs int64  `json:"lastSeenTs"`
}

type Service struct {
	mu        sync.Mutex
	notifyMu  sync.Mutex
	config    Config
	store     telegramStore
	broadcast func([]byte)
	notify    notifications.Creator
	clock     Clock
	jitter    Jitter
	status    Status
	known     map[string]KnownChat
	inflight  map[string]struct{}
	submit    func(context.Context, int64, string, string, string) error
	jobState  func() string
	history   <-chan struct{}

	reference         string
	resolver          secretResolver
	factory           APIFactory
	revisions         chan config.SecretRevision
	cancelGen         context.CancelFunc
	generation        uint64
	root              context.Context
	activeAPI         API
	activeCtx         context.Context
	activeToken       string
	unsubscribe       func()
	connectedNotified bool
}

func cryptoJitter() float64 {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return 0.5
	}
	return float64(binary.LittleEndian.Uint64(raw[:])>>11) / (1 << 53)
}

func New(cfg Config, store telegramStore, broadcast func([]byte), notify notifications.Creator) *Service {
	if broadcast == nil {
		broadcast = func([]byte) {}
	}
	state, detail := "disabled", "Telegram is disabled"
	if cfg.Enabled {
		state, detail = "connecting", "Connecting to Telegram"
	}
	return &Service{
		config: cfg, store: store, broadcast: broadcast, notify: notify,
		clock: realClock{}, jitter: cryptoJitter, known: map[string]KnownChat{},
		inflight: map[string]struct{}{}, revisions: make(chan config.SecretRevision, 1),
		status: Status{
			Enabled: cfg.Enabled, State: state, Detail: detail,
			Poll: PollStatus{TimeoutSeconds: cfg.PollTimeoutSeconds}, MaxEventLog: cfg.MaxEventLog,
		},
	}
}

func (s *Service) ConfigureSecret(reference string, resolver secretResolver, factory APIFactory) {
	s.reference, s.resolver, s.factory = reference, resolver, factory
}

func (s *Service) SetSubmitter(fn func(context.Context, int64, string, string, string) error) {
	s.submit = fn
}
func (s *Service) SetJobState(fn func() string)          { s.jobState = fn }
func (s *Service) SetHistoryReady(ready <-chan struct{}) { s.history = ready }

func Authorized(cfg Config, chatID, userID string, isBot bool) bool {
	if isBot || chatID == "" || userID == "" || !contains(cfg.AllowedChatIDs, chatID) {
		return false
	}
	return len(cfg.AllowedUserIDs) == 0 || contains(cfg.AllowedUserIDs, userID)
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func sanitize(value string, limit int) string {
	out := make([]rune, 0, min(len([]rune(value)), limit))
	for _, char := range value {
		if unicode.IsControl(char) {
			char = '\uFFFD'
		}
		out = append(out, char)
		if len(out) == limit {
			break
		}
	}
	return string(out)
}

func preview(value string, limit int) string {
	clean := make([]rune, 0, len([]rune(value)))
	for _, char := range value {
		if unicode.IsControl(char) && char != '\n' && char != '\t' {
			char = '\uFFFD'
		}
		clean = append(clean, char)
	}
	collapsed := []rune(strings.Join(strings.Fields(string(clean)), " "))
	if len(collapsed) <= limit {
		return string(collapsed)
	}
	if limit <= 1 {
		return "…"
	}
	return string(collapsed[:limit-1]) + "…"
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
	return map[string]string{
		"accepted": "Message accepted", "denied": "Update was not authorized",
		"ignored_edit": "Edited messages are ignored", "ignored_bot": "Bot-authored messages are ignored",
		"ignored_non_text": "Non-text messages are ignored", "ignored_malformed": "Malformed update ignored",
		"ignored_unsupported": "Unsupported update ignored", "command": "Command handled",
		"unknown_command": "Unknown command handled", "rejected_too_long": "Message exceeded 4096 characters",
		"raw_omitted": "Raw update exceeded the retention cap", "offset_reset": "Stored update offset was reset",
		"reply_send_failed": "Final Telegram reply could not be delivered",
		"typing_failed":     "Telegram typing action failed",
	}[outcome]
}

func decodeRawObject(raw []byte) (any, json.RawMessage, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, json.RawMessage(`{}`), false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var object map[string]any
	if decoder.Decode(&object) != nil || object == nil {
		return nil, json.RawMessage(`{}`), false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, json.RawMessage(`{}`), false
	}
	var compact bytes.Buffer
	if json.Compact(&compact, raw) != nil || compact.Len() > 16384 {
		return object, json.RawMessage(`{}`), false
	}
	return object, append(json.RawMessage(nil), compact.Bytes()...), true
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

func NewEventAt(now time.Time, update Update, outcome, activeToken string) Event {
	event := Event{
		ID: jobs.NewID(), Ts: now.UnixMilli(), UpdateID: update.UpdateID, Kind: "message",
		Outcome: outcome, Detail: eventDetail(outcome), RawUpdate: json.RawMessage(`{}`),
	}
	message := update.Message
	if update.EditedMessage != nil {
		message, event.Kind = update.EditedMessage, "edited_message"
	}
	if message != nil {
		if message.Chat != nil {
			event.ChatID = strconv.FormatInt(message.Chat.ID, 10)
			event.ChatType, event.ChatLabel = sanitize(message.Chat.Type, 128), chatLabel(*message.Chat)
		}
		if message.From != nil {
			event.UserID, event.Username = strconv.FormatInt(message.From.ID, 10), sanitize(message.From.Username, 128)
		}
		event.MessageID = message.MessageID
		if outcome == "accepted" || outcome == "rejected_too_long" {
			event.TextPreview = preview(message.Text, 160)
		}
	}
	object, compact, retained := decodeRawObject(update.Raw)
	if retained && (activeToken == "" || !recursiveContains(object, activeToken)) {
		event.RawUpdate = compact
	} else if len(bytes.TrimSpace(update.Raw)) > 0 {
		event.RawOmitted = true
		event.Outcome, event.Detail = "raw_omitted", eventDetail("raw_omitted")
	}
	return event
}

func NewEvent(update Update, outcome, activeToken string) Event {
	return NewEventAt(time.Now(), update, outcome, activeToken)
}

func (s *Service) appendEvent(event Event) error {
	return s.appendEventFor(context.Background(), 0, event)
}

func (s *Service) appendEventFor(ctx context.Context, generation uint64, event Event) error {
	if generation != 0 && !s.generationCurrent(ctx, generation) {
		return context.Canceled
	}
	if s.store == nil {
		return errors.New("storage unavailable")
	}
	encoded, _ := json.Marshal(event)
	reply, err := s.store.Eval(appendEventScript, []string{eventsKey}, string(encoded), strconv.Itoa(s.config.MaxEventLog))
	if err != nil {
		return err
	}
	count, ok := reply.(int64)
	if !ok {
		return errors.New("invalid event append reply")
	}
	if generation != 0 && !s.generationCurrent(ctx, generation) {
		return context.Canceled
	}
	s.updateStatus(generation, func(status *Status) {
		status.EventCount = int(count)
	})
	if generation != 0 && !s.generationCurrent(ctx, generation) {
		return context.Canceled
	}
	frame, _ := json.Marshal(map[string]any{"type": "telegram-event", "event": event})
	s.broadcast(frame)
	return nil
}

func (s *Service) appendSystem(outcome, code string) {
	s.appendSystemFor(context.Background(), 0, outcome, code)
}

func (s *Service) appendSystemFor(ctx context.Context, generation uint64, outcome, code string) {
	event := Event{
		ID: jobs.NewID(), Ts: s.clock.Now().UnixMilli(), Kind: "system", Outcome: outcome,
		Detail: eventDetail(outcome), RawUpdate: json.RawMessage(`{}`),
	}
	if event.Detail == "" {
		event.Detail = sanitize(code, 256)
	}
	if err := s.appendEventFor(ctx, generation, event); err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("telegram: event: storage_unavailable")
	}
}

func (s *Service) loadEvents(limit int) []Event {
	if s.store == nil {
		return nil
	}
	rows, err := s.store.LRange(eventsKey, -limit, -1)
	if err != nil {
		return nil
	}
	result := make([]Event, 0, len(rows))
	for _, row := range rows {
		var event Event
		if json.Unmarshal([]byte(row), &event) == nil {
			result = append(result, event)
		}
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

func (s *Service) StatusMessage() []byte {
	s.mu.Lock()
	status := s.statusLocked()
	s.mu.Unlock()
	return statusMessage(status)
}

func statusMessage(status Status) []byte {
	body, _ := json.Marshal(map[string]any{"type": "telegram-status", "status": status})
	return body
}

func (s *Service) statusLocked() Status {
	status := s.status
	status.Destinations = s.destinationsLocked()
	return status
}

func (s *Service) updateStatus(generation uint64, mutate func(*Status)) bool {
	s.mu.Lock()
	if generation != 0 && generation != s.generation {
		s.mu.Unlock()
		return false
	}
	before := s.statusLocked()
	mutate(&s.status)
	after := s.statusLocked()
	changed := !reflect.DeepEqual(before, after)
	s.mu.Unlock()
	if changed {
		if generation != 0 && !s.generationCurrent(context.Background(), generation) {
			return false
		}
		s.broadcast(statusMessage(after))
	}
	return true
}

func (s *Service) EventsMessage() []byte {
	events := s.loadEvents(50)
	body, _ := json.Marshal(map[string]any{"type": "telegram-events", "events": summaries(events), "eventCount": s.eventCount()})
	return body
}

func (s *Service) destinationsLocked() []Destination {
	result := make([]Destination, 0, len(s.config.AllowedChatIDs))
	for _, id := range s.config.AllowedChatIDs {
		if known, ok := s.known[id]; ok {
			result = append(result, Destination{ChatID: id, Label: known.Label, Type: known.Type, Observed: true})
		} else {
			result = append(result, Destination{ChatID: id, Label: "Chat " + id})
		}
	}
	return result
}

func (s *Service) loadPersistent() error {
	if s.store == nil {
		return errors.New("storage unavailable")
	}
	offset := int64(0)
	raw, err := s.store.Get(offsetKey)
	if err != nil {
		return err
	}
	if raw != nil {
		parsed, parseErr := strconv.ParseInt(*raw, 10, 64)
		if parseErr == nil && parsed >= 0 {
			offset = parsed
		} else {
			s.appendSystem("offset_reset", "offset_reset")
		}
	}
	rows, err := s.store.LRange(chatsKey, 0, -1)
	if err != nil {
		return err
	}
	known := map[string]KnownChat{}
	for _, row := range rows {
		var item KnownChat
		if json.Unmarshal([]byte(row), &item) == nil && canonicalChatID.MatchString(item.ChatID) {
			known[item.ChatID] = item
		}
	}
	s.mu.Lock()
	s.known = known
	s.status.Poll.NextOffset, s.status.EventCount = offset, s.eventCount()
	s.mu.Unlock()
	return nil
}

func (s *Service) Start(ctx context.Context) {
	s.root = ctx
	if !s.config.Enabled {
		_ = s.loadPersistent()
		if s.store != nil {
			_, _ = s.store.HDel(notificationKey, "auth_active", "suspended_active", "suspended_conflict", "delivery_active")
		}
		return
	}
	if s.resolver == nil || s.factory == nil || s.reference == "" {
		s.setState(0, "secret_unavailable", "secret_unavailable")
		return
	}
	s.unsubscribe = s.resolver.Subscribe(s.reference, func(revision config.SecretRevision) {
		s.clearAuthEligibility()
		s.mu.Lock()
		cancel := s.cancelGen
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		select {
		case s.revisions <- revision:
		default:
			select {
			case <-s.revisions:
			default:
			}
			select {
			case s.revisions <- revision:
			default:
			}
		}
	})
	go s.bootstrap(ctx)
}

func (s *Service) clearAuthEligibility() {
	s.notifyMu.Lock()
	defer s.notifyMu.Unlock()
	if s.store != nil {
		_, _ = s.store.HDel(notificationKey, "auth_active")
	}
}

func (s *Service) bootstrap(ctx context.Context) {
	for failures := 1; ; failures++ {
		if err := s.loadPersistent(); err == nil {
			s.lifecycle(ctx)
			return
		}
		s.setState(0, "backing_off", "storage_unavailable")
		if s.clock.Sleep(ctx, backoff(failures, s.jitter())) != nil {
			s.setState(0, "stopped", "")
			return
		}
	}
}

func (s *Service) lifecycle(ctx context.Context) {
	defer func() {
		if s.unsubscribe != nil {
			s.unsubscribe()
		}
		s.mu.Lock()
		s.activeToken, s.activeAPI, s.activeCtx = "", nil, nil
		s.mu.Unlock()
		s.setState(0, "stopped", "")
	}()
	revision := config.SecretRevision{Success: true}
	for ctx.Err() == nil {
		if !revision.Success {
			s.setState(0, "secret_unavailable", "secret_unavailable")
			select {
			case <-ctx.Done():
				return
			case revision = <-s.revisions:
				continue
			}
		}
		tokenBytes, _, err := s.resolver.Resolve(s.reference, 5*time.Minute)
		if err != nil || len(tokenBytes) == 0 {
			zero(tokenBytes)
			s.setState(0, "secret_unavailable", "secret_unavailable")
			select {
			case <-ctx.Done():
				return
			case revision = <-s.revisions:
				continue
			}
		}
		token := string(tokenBytes)
		zero(tokenBytes)
		api, err := s.factory(token)
		if err != nil {
			s.setState(0, "secret_unavailable", "secret_unavailable")
			return
		}
		genCtx, cancel := context.WithCancel(ctx)
		s.mu.Lock()
		s.generation++
		generation := s.generation
		s.cancelGen, s.activeAPI, s.activeCtx, s.activeToken = cancel, api, genCtx, token
		s.mu.Unlock()
		s.setState(generation, "connecting", "")
		authFailed := s.runGeneration(genCtx, generation, api)
		cancel()
		s.mu.Lock()
		if s.generation == generation {
			s.cancelGen, s.activeAPI, s.activeCtx, s.activeToken = nil, nil, nil, ""
		}
		s.mu.Unlock()
		if ctx.Err() != nil {
			return
		}
		if authFailed {
			select {
			case <-ctx.Done():
				return
			case revision = <-s.revisions:
			}
		} else {
			select {
			case revision = <-s.revisions:
			default:
				revision = config.SecretRevision{Success: true}
			}
		}
	}
}

func (s *Service) generationCurrent(ctx context.Context, generation uint64) bool {
	if ctx != nil && ctx.Err() != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return generation == s.generation && s.root != nil && s.root.Err() == nil &&
		s.activeCtx != nil && s.activeCtx.Err() == nil
}

func (s *Service) runGeneration(ctx context.Context, generation uint64, api API) bool {
	failures := 0
	var failureStarted time.Time
	for ctx.Err() == nil {
		if !s.generationCurrent(ctx, generation) {
			return false
		}
		me, err := api.GetMe(ctx)
		if !s.generationCurrent(ctx, generation) {
			return false
		}
		if err == nil && !me.IsBot {
			err = &APIError{Code: "identity_not_bot"}
		}
		if err != nil {
			code := errorCode(err)
			if code == "authentication_failed" || code == "identity_not_bot" {
				s.setState(generation, "auth_failed", code)
				s.authNotification(ctx)
				return true
			}
			failures++
			if failureStarted.IsZero() {
				failureStarted = s.clock.Now()
			}
			if !s.retry(ctx, generation, failures, code, err, failureStarted) {
				return false
			}
			continue
		}
		if !s.generationCurrent(ctx, generation) {
			return false
		}
		s.updateStatus(generation, func(status *Status) {
			status.Bot = BotIdentity{
				ID: strconv.FormatInt(me.ID, 10), Username: sanitize(me.Username, 128),
				DisplayName: sanitize(strings.TrimSpace(me.FirstName+" "+me.LastName), 128),
			}
			status.Poll.ConsecutiveFailures = 0
			status.Poll.RetryAt = nil
			status.State, status.Code, status.Detail = "connected", "", stateDetail("connected")
		})
		failures, failureStarted = 0, time.Time{}
		s.connectedNotification(ctx)
		break
	}
	for ctx.Err() == nil {
		s.mu.Lock()
		offset := s.status.Poll.NextOffset
		s.mu.Unlock()
		if !s.generationCurrent(ctx, generation) {
			return false
		}
		updates, err := api.GetUpdates(ctx, GetUpdatesRequest{
			Offset: offset, Timeout: s.config.PollTimeoutSeconds,
			AllowedUpdates: []string{"message", "edited_message"},
		})
		if !s.generationCurrent(ctx, generation) {
			return false
		}
		if err != nil {
			code := errorCode(err)
			if code == "authentication_failed" {
				s.setState(generation, "auth_failed", code)
				s.authNotification(ctx)
				return true
			}
			failures++
			if failureStarted.IsZero() {
				failureStarted = s.clock.Now()
			}
			if !s.retry(ctx, generation, failures, code, err, failureStarted) {
				return false
			}
			continue
		}
		if !s.generationCurrent(ctx, generation) {
			return false
		}
		now := s.clock.Now()
		wasSuspended := s.state() == "polling_suspended"
		failures, failureStarted = 0, time.Time{}
		s.updateStatus(generation, func(status *Status) {
			status.Poll.ConsecutiveFailures = 0
			status.Poll.RetryAt = nil
			status.Poll.LastSuccessTs = now.UnixMilli()
			status.State, status.Code, status.Detail = "connected", "", stateDetail("connected")
		})
		if wasSuspended {
			s.recoveredNotification(ctx)
		}
		sort.Slice(updates, func(i, j int) bool { return updates[i].UpdateID < updates[j].UpdateID })
		storageFailure := false
		for _, update := range updates {
			if !s.generationCurrent(ctx, generation) {
				return false
			}
			if update.UpdateID < offset {
				continue
			}
			if err := s.process(ctx, generation, api, update); err != nil {
				if ctx.Err() != nil || errors.Is(err, context.Canceled) {
					return false
				}
				storageFailure = true
				break
			}
			candidate := update.UpdateID + 1
			if !s.generationCurrent(ctx, generation) {
				return false
			}
			if s.store == nil || s.store.Set(offsetKey, strconv.FormatInt(candidate, 10)) != nil {
				storageFailure = true
				break
			}
			offset = candidate
			if !s.generationCurrent(ctx, generation) {
				return false
			}
			s.updateStatus(generation, func(status *Status) {
				status.Poll.NextOffset = candidate
			})
		}
		if storageFailure {
			failures++
			if failureStarted.IsZero() {
				failureStarted = s.clock.Now()
			}
			if !s.retry(ctx, generation, failures, "storage_unavailable", nil, failureStarted) {
				return false
			}
		}
	}
	return false
}

func (s *Service) retry(ctx context.Context, generation uint64, failures int, code string, err error, failureStarted time.Time) bool {
	if !s.generationCurrent(ctx, generation) {
		return false
	}
	now := s.clock.Now()
	suspended := s.state() == "polling_suspended" || code == "poll_conflict" || failures >= 5 ||
		(!failureStarted.IsZero() && now.Sub(failureStarted) >= 60*time.Second)
	state := "backing_off"
	if suspended {
		state = "polling_suspended"
	}
	delay := backoff(failures, s.jitter())
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Code == "rate_limited" && apiErr.RetryAfter > 0 {
		delay = min(max(apiErr.RetryAfter, time.Second), 15*time.Minute)
	}
	retryAt := now.Add(delay).UnixMilli()
	s.updateStatus(generation, func(status *Status) {
		status.Poll.ConsecutiveFailures = failures
		status.Poll.RetryAt = &retryAt
		status.State, status.Code, status.Detail = state, code, stateDetail(state)
	})
	s.appendSystemFor(ctx, generation, code, code)
	if suspended {
		s.suspendedNotification(ctx, code == "poll_conflict")
	}
	if !suspended && !failureStarted.IsZero() {
		untilSuspended := 60*time.Second - now.Sub(failureStarted)
		if untilSuspended > 0 && untilSuspended < delay {
			if s.clock.Sleep(ctx, untilSuspended) != nil || !s.generationCurrent(ctx, generation) {
				return false
			}
			s.updateStatus(generation, func(status *Status) {
				status.State, status.Detail = "polling_suspended", stateDetail("polling_suspended")
			})
			s.suspendedNotification(ctx, false)
			delay -= untilSuspended
		}
	}
	return s.clock.Sleep(ctx, delay) == nil && s.generationCurrent(ctx, generation)
}

func (s *Service) state() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status.State
}

func (s *Service) setState(generation uint64, state, code string) {
	s.updateStatus(generation, func(status *Status) {
		status.State, status.Code, status.Detail = state, code, stateDetail(state)
	})
}

func stateDetail(state string) string {
	return map[string]string{
		"disabled": "Telegram is disabled", "invalid_config": "Telegram configuration is invalid",
		"secret_unavailable": "Telegram bot-token secret is unavailable",
		"connecting":         "Connecting to Telegram", "connected": "Polling for authorized messages",
		"backing_off":       "Retrying Telegram connection",
		"polling_suspended": "Telegram polling is suspended; retries continue",
		"auth_failed":       "Telegram authentication failed", "stopped": "Telegram service stopped",
	}[state]
}

func (s *Service) process(ctx context.Context, generation uint64, api API, update Update) error {
	if !s.generationCurrent(ctx, generation) {
		return context.Canceled
	}
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
			if err := s.observe(ctx, generation, *message.Chat); err != nil {
				return err
			}
			text := strings.TrimSpace(message.Text)
			switch {
			case text == "":
				outcome = "ignored_non_text"
			case commandName(*message, s.botUsername()) != "":
				outcome = s.handleCommand(ctx, generation, api, chatID, commandName(*message, s.botUsername()))
			case len([]rune(text)) > 4096:
				outcome = "rejected_too_long"
				if err := s.send(ctx, generation, api, chatID, "That message is too long. Please keep it to 4096 characters."); err != nil &&
					!errors.Is(err, context.Canceled) {
					s.deliveryFailed(ctx)
				}
			default:
				if s.history != nil {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-s.history:
					}
				}
				outcome = "accepted"
				if s.submit == nil {
					return errors.New("chat submitter unavailable")
				}
				if !s.generationCurrent(ctx, generation) {
					return context.Canceled
				}
				if err := s.submit(ctx, update.UpdateID, chatID, userID, text); err != nil {
					if !s.generationCurrent(ctx, generation) {
						return context.Canceled
					}
					_ = s.send(ctx, generation, api, chatID, "Virtual Me could not queue that request. Please try again shortly.")
					return err
				}
			}
		}
	}
	s.mu.Lock()
	token := ""
	if generation == s.generation {
		token = s.activeToken
	}
	s.mu.Unlock()
	return s.appendEventFor(ctx, generation, NewEventAt(s.clock.Now(), update, outcome, token))
}

func (s *Service) botUsername() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status.Bot.Username
}

func (s *Service) observe(ctx context.Context, generation uint64, chat Chat) error {
	if !s.generationCurrent(ctx, generation) {
		return context.Canceled
	}
	item := KnownChat{
		ChatID: strconv.FormatInt(chat.ID, 10), Type: sanitize(chat.Type, 128),
		Label: chatLabel(chat), Username: sanitize(chat.Username, 128), LastSeenTs: s.clock.Now().UnixMilli(),
	}
	encoded, _ := json.Marshal(item)
	if s.store == nil {
		return errors.New("storage unavailable")
	}
	if _, err := s.store.Eval(upsertChatScript, []string{chatsKey}, item.ChatID, string(encoded)); err != nil {
		return err
	}
	if !s.generationCurrent(ctx, generation) {
		return context.Canceled
	}
	s.mu.Lock()
	before := s.statusLocked()
	s.known[item.ChatID] = item
	after := s.statusLocked()
	s.mu.Unlock()
	if !reflect.DeepEqual(before, after) {
		if !s.generationCurrent(ctx, generation) {
			return context.Canceled
		}
		s.broadcast(statusMessage(after))
	}
	return nil
}

func utf16Prefix(text string, units int) (string, bool) {
	if units <= 0 {
		return "", false
	}
	count := 0
	for index, char := range text {
		width := len(utf16.Encode([]rune{char}))
		if count+width > units {
			return "", false
		}
		count += width
		if count == units {
			return text[:index+len(string(char))], true
		}
	}
	return "", false
}

func commandName(message Message, botUsername string) string {
	var entity *MessageEntity
	for index := range message.Entities {
		if message.Entities[index].Type == "bot_command" && message.Entities[index].Offset == 0 {
			entity = &message.Entities[index]
			break
		}
	}
	if entity == nil {
		return ""
	}
	value, ok := utf16Prefix(message.Text, entity.Length)
	if !ok {
		return "unknown"
	}
	value = strings.ToLower(value)
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

func (s *Service) handleCommand(ctx context.Context, generation uint64, api API, chatID, command string) string {
	text, outcome := "Unknown command. Commands: /help, /status.", "unknown_command"
	switch command {
	case "help":
		text, outcome = "Virtual Me shares one conversation with the web console. Send text to ask a question. Commands: /help, /status.", "command"
	case "status":
		state := "idle"
		if s.jobState != nil {
			state = s.jobState()
		}
		text, outcome = fmt.Sprintf("Virtual Me is %s. Telegram is connected as @%s.", state, s.botUsername()), "command"
	}
	if err := s.send(ctx, generation, api, chatID, text); err != nil && !errors.Is(err, context.Canceled) {
		s.deliveryFailed(ctx)
	}
	return outcome
}

func (s *Service) send(ctx context.Context, generation uint64, api API, chatID, text string) error {
	for _, chunk := range ChunkText(text) {
		if !s.generationCurrent(ctx, generation) {
			return context.Canceled
		}
		if _, err := api.SendMessage(ctx, SendMessageRequest{ChatID: chatID, Text: chunk}); err != nil {
			return err
		}
	}
	if !s.generationCurrent(ctx, generation) {
		return context.Canceled
	}
	s.deliverySucceeded()
	return nil
}

func (s *Service) Typing(ctx context.Context, chatID string) {
	send := func() {
		s.mu.Lock()
		api := s.activeAPI
		generation := s.generation
		generationCtx := s.activeCtx
		s.mu.Unlock()
		if api != nil && generationCtx != nil {
			callCtx, cancel := linkedContext(ctx, generationCtx)
			defer cancel()
			if !s.generationCurrent(callCtx, generation) {
				return
			}
			if err := api.SendChatAction(callCtx, SendChatActionRequest{ChatID: chatID, Action: "typing"}); err != nil &&
				callCtx.Err() == nil {
				s.appendSystemFor(callCtx, generation, "typing_failed", "send_failed")
			}
		}
	}
	send()
	ticker := time.NewTicker(4 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			send()
		}
	}
}

func (s *Service) Deliver(ctx context.Context, chatID, text string, cause error, stopped bool) error {
	s.mu.Lock()
	api := s.activeAPI
	generation := s.generation
	generationCtx := s.activeCtx
	s.mu.Unlock()
	if api == nil || generationCtx == nil {
		return errors.New("Telegram is not connected")
	}
	callCtx, cancel := linkedContext(ctx, generationCtx)
	defer cancel()
	switch {
	case stopped:
		text = "That request was cancelled before it completed."
	case cause != nil:
		text = "Virtual Me could not complete that request. Please try again."
	}
	err := s.send(callCtx, generation, api, chatID, text)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return err
		}
		s.appendSystem("reply_send_failed", "send_failed")
		s.deliveryFailed(ctx)
	}
	return err
}

func linkedContext(parent, generation context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(generation, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

func (s *Service) notificationState() map[string]string {
	if s.store == nil {
		return map[string]string{}
	}
	state, err := s.store.HGetAll(notificationKey)
	if err != nil {
		return map[string]string{}
	}
	return state
}

func (s *Service) createNotification(ctx context.Context, severity, title, summary string) error {
	if s.notify == nil || ctx.Err() != nil {
		return ctx.Err()
	}
	if _, err := s.notify.Create(ctx, notifications.CreateRequest{
		Type: severity, Sender: "telegram", Title: title, Summary: summary, Renderer: "generic",
	}); err != nil {
		log.Printf("telegram: notification: storage_unavailable")
		return err
	}
	return nil
}

func (s *Service) connectedNotification(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	s.notifyMu.Lock()
	defer s.notifyMu.Unlock()
	s.mu.Lock()
	if s.connectedNotified {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	if err := s.createNotification(ctx, "info", "Telegram connected", "The configured bot is authenticated and long polling is active."); err != nil {
		return
	}
	s.mu.Lock()
	s.connectedNotified = true
	s.mu.Unlock()
	if s.store != nil {
		_, _ = s.store.HDel(notificationKey, "auth_active")
	}
}

func (s *Service) authNotification(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	s.notifyMu.Lock()
	defer s.notifyMu.Unlock()
	if s.store == nil {
		return
	}
	state := s.notificationState()
	if state["auth_active"] == "1" {
		return
	}
	if _, err := s.store.HSet(notificationKey, "auth_active", "1"); err != nil {
		return
	}
	if err := s.createNotification(ctx, "error", "Telegram authentication failed", "Update the Telegram bot-token secret in Config."); err != nil {
		_, _ = s.store.HDel(notificationKey, "auth_active")
	}
}

func (s *Service) suspendedNotification(ctx context.Context, conflict bool) {
	if ctx.Err() != nil {
		return
	}
	s.notifyMu.Lock()
	defer s.notifyMu.Unlock()
	if s.store == nil {
		return
	}
	state := s.notificationState()
	now := s.clock.Now().UnixMilli()
	last, _ := strconv.ParseInt(state["suspended_last"], 10, 64)
	if conflict && state["suspended_conflict"] != "1" {
		if _, err := s.store.HSet(
			notificationKey, "suspended_active", "1", "suspended_conflict", "1",
			"suspended_last", strconv.FormatInt(now, 10),
		); err != nil {
			return
		}
		if err := s.createNotification(ctx, "warning", "Telegram polling suspended", "Another consumer is polling this bot. Stop the other poller; automatic retries continue."); err != nil {
			_, _ = s.store.HDel(notificationKey, "suspended_active", "suspended_conflict", "suspended_last")
		}
		return
	}
	if state["suspended_active"] == "1" || now-last < int64(15*time.Minute/time.Millisecond) {
		return
	}
	if _, err := s.store.HSet(notificationKey, "suspended_active", "1", "suspended_last", strconv.FormatInt(now, 10)); err != nil {
		return
	}
	body := "Telegram updates are temporarily unavailable; automatic retries continue."
	if err := s.createNotification(ctx, "warning", "Telegram polling suspended", body); err != nil {
		_, _ = s.store.HDel(notificationKey, "suspended_active", "suspended_last")
	}
}

func (s *Service) recoveredNotification(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	s.notifyMu.Lock()
	defer s.notifyMu.Unlock()
	if s.store == nil {
		return
	}
	state := s.notificationState()
	if state["suspended_active"] != "1" {
		return
	}
	_, _ = s.store.HDel(notificationKey, "suspended_active", "suspended_conflict")
	_ = s.createNotification(ctx, "info", "Telegram polling recovered", "Telegram updates are available again.")
}

func (s *Service) deliveryFailed(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	s.notifyMu.Lock()
	defer s.notifyMu.Unlock()
	if s.store == nil {
		return
	}
	if s.state() == "auth_failed" {
		return
	}
	state := s.notificationState()
	now := s.clock.Now().UnixMilli()
	last, _ := strconv.ParseInt(state["delivery_last"], 10, 64)
	if now-last < int64(15*time.Minute/time.Millisecond) {
		return
	}
	if _, err := s.store.HSet(notificationKey, "delivery_active", "1", "delivery_last", strconv.FormatInt(now, 10)); err != nil {
		return
	}
	if err := s.createNotification(ctx, "warning", "Telegram delivery failed", "A Telegram message could not be delivered. Check the Telegram integration page."); err != nil {
		_, _ = s.store.HDel(notificationKey, "delivery_active", "delivery_last")
	}
}

func (s *Service) deliverySucceeded() {
	s.notifyMu.Lock()
	defer s.notifyMu.Unlock()
	if s.store != nil {
		_, _ = s.store.HDel(notificationKey, "delivery_active")
	}
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

func (s *Service) HandleMessage(conn *ws.Conn, payload []byte) bool {
	var envelope struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(payload, &envelope) != nil || !strings.HasPrefix(envelope.Type, "telegram-") {
		return false
	}
	switch envelope.Type {
	case "telegram-status-req":
		var request struct {
			Type string `json:"type"`
		}
		if decodeStrict(payload, &request) == nil {
			_ = conn.WriteText(s.StatusMessage())
		}
	case "telegram-events-req":
		var request struct {
			Type string `json:"type"`
		}
		if decodeStrict(payload, &request) == nil {
			_ = conn.WriteText(s.EventsMessage())
		}
	case "telegram-event-detail-req":
		s.handleDetail(conn, payload)
	case "telegram-test-send":
		s.handleTestSend(conn, payload)
	default:
		return false
	}
	return true
}

func (s *Service) handleDetail(conn *ws.Conn, payload []byte) {
	var request struct {
		Type      string `json:"type"`
		RequestID string `json:"requestId"`
		ID        string `json:"id"`
	}
	if decodeStrict(payload, &request) != nil || !requestID.MatchString(request.RequestID) || request.ID == "" {
		return
	}
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
		Type   string `json:"type"`
		ID     string `json:"id"`
		ChatID string `json:"chatId"`
		Text   string `json:"text"`
	}
	errorText := ""
	if decodeStrict(payload, &request) != nil || !requestID.MatchString(request.ID) ||
		strings.TrimSpace(request.Text) == "" || len([]rune(request.Text)) > 4096 {
		errorText = "Message must be 1–4096 characters"
	} else if !canonicalChatID.MatchString(request.ChatID) || !contains(s.config.AllowedChatIDs, request.ChatID) {
		errorText = "Destination is not authorized"
	}
	if errorText != "" {
		s.writeCommandResult(conn, request.ID, errorText)
		return
	}
	s.mu.Lock()
	connected := s.status.State == "connected"
	generation := s.generation
	generationCtx := s.activeCtx
	_, duplicate := s.inflight[request.ID]
	if connected && generationCtx != nil && !duplicate {
		s.inflight[request.ID] = struct{}{}
	}
	s.mu.Unlock()
	if duplicate {
		s.writeCommandResult(conn, request.ID, "Request is already running")
		return
	}
	if !connected || generationCtx == nil {
		s.writeCommandResult(conn, request.ID, "Telegram is not connected")
		return
	}
	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.inflight, request.ID)
			s.mu.Unlock()
		}()
		s.mu.Lock()
		api := s.activeAPI
		s.mu.Unlock()
		sendErr := errors.New("not connected")
		if api != nil {
			sendErr = s.send(generationCtx, generation, api, request.ChatID, request.Text)
		}
		if sendErr != nil {
			if errors.Is(sendErr, context.Canceled) {
				s.writeCommandResult(conn, request.ID, "Telegram is not connected")
				return
			}
			s.deliveryFailed(generationCtx)
			s.writeCommandResult(conn, request.ID, "Telegram could not send the test message")
			return
		}
		s.writeCommandResult(conn, request.ID, "")
	}()
}

func (s *Service) writeCommandResult(conn *ws.Conn, id, errorText string) {
	frame, _ := json.Marshal(map[string]any{"type": "telegram-command-result", "id": id, "ok": errorText == "", "error": errorText})
	_ = conn.WriteText(frame)
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

func zero(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
