package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mayanklahiri/virtualme/controller/internal/config"
	"github.com/mayanklahiri/virtualme/controller/internal/notifications"
)

type fakeNotificationCreator struct {
	mu       sync.Mutex
	requests []notifications.CreateRequest
	failures int
}

func (f *fakeNotificationCreator) Create(_ context.Context, request notifications.CreateRequest) (notifications.Notification, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, request)
	if f.failures > 0 {
		f.failures--
		return notifications.Notification{}, errors.New("injected notification failure")
	}
	return notifications.Notification{}, nil
}

func TestServiceAcceptsNarrowNotificationCreator(t *testing.T) {
	service := New(Config{}, nil, nil, &fakeNotificationCreator{})
	if service.notify == nil {
		t.Fatal("notification creator was not injected")
	}
}

type memoryStore struct {
	mu          sync.Mutex
	values      map[string]string
	lists       map[string][]string
	hashes      map[string]map[string]string
	failSetOnce bool
}

func newMemoryStore() *memoryStore {
	return &memoryStore{values: map[string]string{}, lists: map[string][]string{}, hashes: map[string]map[string]string{}}
}

func (s *memoryStore) Get(key string) (*string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.values[key]
	if !ok {
		return nil, nil
	}
	return &value, nil
}
func (s *memoryStore) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failSetOnce {
		s.failSetOnce = false
		return errors.New("injected")
	}
	s.values[key] = value
	return nil
}
func (s *memoryStore) LRange(key string, start, stop int) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	values := s.lists[key]
	if start < 0 {
		start += len(values)
	}
	if stop < 0 {
		stop += len(values)
	}
	start, stop = max(0, start), min(len(values)-1, stop)
	if len(values) == 0 || start > stop || start >= len(values) {
		return nil, nil
	}
	return append([]string(nil), values[start:stop+1]...), nil
}
func (s *memoryStore) LLen(key string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return int64(len(s.lists[key])), nil
}
func (s *memoryStore) Eval(script string, keys []string, args ...string) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch script {
	case appendEventScript:
		s.lists[keys[0]] = append(s.lists[keys[0]], args[0])
		capacity, _ := strconv.Atoi(args[1])
		if len(s.lists[keys[0]]) > capacity {
			s.lists[keys[0]] = s.lists[keys[0]][len(s.lists[keys[0]])-capacity:]
		}
		return int64(len(s.lists[keys[0]])), nil
	case upsertChatScript:
		filtered := s.lists[keys[0]][:0]
		for _, row := range s.lists[keys[0]] {
			var item KnownChat
			if json.Unmarshal([]byte(row), &item) != nil || item.ChatID != args[0] {
				filtered = append(filtered, row)
			}
		}
		s.lists[keys[0]] = append(filtered, args[1])
		if len(s.lists[keys[0]]) > 200 {
			s.lists[keys[0]] = s.lists[keys[0]][len(s.lists[keys[0]])-200:]
		}
		return int64(1), nil
	}
	return nil, errors.New("unknown script")
}
func (s *memoryStore) HGetAll(key string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := map[string]string{}
	for field, value := range s.hashes[key] {
		result[field] = value
	}
	return result, nil
}
func (s *memoryStore) HSet(key string, values ...string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hashes[key] == nil {
		s.hashes[key] = map[string]string{}
	}
	for index := 0; index < len(values); index += 2 {
		s.hashes[key][values[index]] = values[index+1]
	}
	return int64(len(values) / 2), nil
}
func (s *memoryStore) HDel(key string, fields ...string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var removed int64
	for _, field := range fields {
		if _, ok := s.hashes[key][field]; ok {
			delete(s.hashes[key], field)
			removed++
		}
	}
	return removed, nil
}

type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	sleeps []time.Duration
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}
func (c *fakeClock) Sleep(ctx context.Context, delay time.Duration) error {
	c.mu.Lock()
	c.sleeps = append(c.sleeps, delay)
	c.now = c.now.Add(delay)
	c.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

type offsetAPI struct {
	mu      sync.Mutex
	offsets []int64
}

func (a *offsetAPI) GetMe(context.Context) (User, error) {
	return User{ID: 1, IsBot: true, Username: "vm"}, nil
}
func (a *offsetAPI) GetUpdates(ctx context.Context, request GetUpdatesRequest) ([]Update, error) {
	a.mu.Lock()
	a.offsets = append(a.offsets, request.Offset)
	call := len(a.offsets)
	a.mu.Unlock()
	if call <= 2 {
		return []Update{
			{UpdateID: 10, Message: &Message{Chat: &Chat{ID: 9}, From: &User{ID: 8}}},
			{UpdateID: 11, Message: &Message{Chat: &Chat{ID: 9}, From: &User{ID: 8}}},
		}, nil
	}
	<-ctx.Done()
	return nil, ctx.Err()
}
func (*offsetAPI) SendMessage(context.Context, SendMessageRequest) (Message, error) {
	return Message{}, nil
}
func (*offsetAPI) SendChatAction(context.Context, SendChatActionRequest) error { return nil }

func TestOffsetPersistenceFailureRetriesOldOffset(t *testing.T) {
	store := newMemoryStore()
	store.failSetOnce = true
	clock := &fakeClock{now: time.Unix(100, 0)}
	api := &offsetAPI{}
	service := New(Config{Enabled: true, PollTimeoutSeconds: 30, MaxEventLog: 20}, store, nil, nil)
	service.clock, service.jitter = clock, func() float64 { return 0.5 }
	ctx, cancel := context.WithCancel(context.Background())
	service.root, service.activeCtx, service.generation, service.activeToken = ctx, ctx, 1, "fake"
	done := make(chan struct{})
	go func() {
		service.runGeneration(ctx, 1, api)
		close(done)
	}()
	deadline := time.After(time.Second)
	for {
		api.mu.Lock()
		calls := len(api.offsets)
		api.mu.Unlock()
		if calls >= 3 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("poll did not retry")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	<-done
	api.mu.Lock()
	offsets := append([]int64(nil), api.offsets...)
	api.mu.Unlock()
	if len(offsets) < 3 || offsets[0] != 0 || offsets[1] != 0 || offsets[2] != 12 {
		t.Fatalf("poll offsets = %v", offsets)
	}
	value, _ := store.Get(offsetKey)
	if value == nil || *value != "12" {
		t.Fatalf("persisted offset = %v", value)
	}
}

type fakeResolver struct {
	mu       sync.Mutex
	value    string
	callback func(config.SecretRevision)
}

func (r *fakeResolver) Resolve(string, time.Duration) ([]byte, config.SecretStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.value == "" {
		return nil, config.SecretStatus{}, errors.New("unavailable")
	}
	return []byte(r.value), config.SecretStatus{Resolved: true}, nil
}
func (r *fakeResolver) Subscribe(_ string, callback func(config.SecretRevision)) func() {
	r.mu.Lock()
	r.callback = callback
	r.mu.Unlock()
	return func() {}
}
func (r *fakeResolver) refresh(value string, success bool) {
	r.mu.Lock()
	r.value = value
	callback := r.callback
	r.mu.Unlock()
	callback(config.SecretRevision{Revision: 2, Success: success})
}

type blockingAPI struct {
	started  chan struct{}
	canceled chan struct{}
	authErr  error
	once     sync.Once
}

func (a *blockingAPI) GetMe(context.Context) (User, error) {
	if a.authErr != nil {
		return User{}, a.authErr
	}
	return User{ID: 1, IsBot: true, Username: "vm"}, nil
}
func (a *blockingAPI) GetUpdates(ctx context.Context, _ GetUpdatesRequest) ([]Update, error) {
	a.once.Do(func() { close(a.started) })
	<-ctx.Done()
	close(a.canceled)
	return nil, ctx.Err()
}
func (*blockingAPI) SendMessage(context.Context, SendMessageRequest) (Message, error) {
	return Message{}, nil
}
func (*blockingAPI) SendChatAction(context.Context, SendChatActionRequest) error { return nil }

func TestSecretRevisionCancelsAndReauthenticatesGeneration(t *testing.T) {
	resolver := &fakeResolver{value: "first"}
	first := &blockingAPI{started: make(chan struct{}), canceled: make(chan struct{})}
	second := &blockingAPI{started: make(chan struct{}), canceled: make(chan struct{})}
	service := New(Config{Enabled: true, PollTimeoutSeconds: 30, MaxEventLog: 20}, newMemoryStore(), nil, nil)
	service.ConfigureSecret("${env:TOKEN}", resolver, func(token string) (API, error) {
		if token == "first" {
			return first, nil
		}
		if token == "second" {
			return second, nil
		}
		return nil, errors.New("wrong token")
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.Start(ctx)
	select {
	case <-first.started:
	case <-time.After(time.Second):
		t.Fatal("first generation did not poll")
	}
	resolver.refresh("second", true)
	select {
	case <-first.canceled:
	case <-time.After(time.Second):
		t.Fatal("first generation was not canceled")
	}
	select {
	case <-second.started:
	case <-time.After(time.Second):
		t.Fatal("replacement generation did not authenticate")
	}
	cancel()
}

type staleAPI struct {
	updatesStarted chan struct{}
	releaseUpdates chan struct{}
	sendsMu        sync.Mutex
	sends          int
}

func (*staleAPI) GetMe(context.Context) (User, error) {
	return User{ID: 1, IsBot: true, Username: "vm"}, nil
}
func (a *staleAPI) GetUpdates(context.Context, GetUpdatesRequest) ([]Update, error) {
	close(a.updatesStarted)
	<-a.releaseUpdates
	return []Update{{UpdateID: 1, Message: &Message{
		Text: "stale", Chat: &Chat{ID: 42}, From: &User{ID: 7},
	}}}, nil
}
func (a *staleAPI) SendMessage(context.Context, SendMessageRequest) (Message, error) {
	a.sendsMu.Lock()
	a.sends++
	a.sendsMu.Unlock()
	return Message{}, nil
}
func (*staleAPI) SendChatAction(context.Context, SendChatActionRequest) error { return nil }

func TestCanceledGenerationHasNoPostCancellationSideEffects(t *testing.T) {
	store := newMemoryStore()
	api := &staleAPI{updatesStarted: make(chan struct{}), releaseUpdates: make(chan struct{})}
	submits := 0
	var broadcasts atomic.Int32
	service := New(Config{
		Enabled: true, PollTimeoutSeconds: 30, MaxEventLog: 20, AllowedChatIDs: []string{"42"},
	}, store, func([]byte) { broadcasts.Add(1) }, nil)
	service.submit = func(context.Context, int64, string, string, string) error {
		submits++
		return nil
	}
	root, stopRoot := context.WithCancel(context.Background())
	defer stopRoot()
	generationCtx, cancelGeneration := context.WithCancel(root)
	service.root, service.activeCtx, service.generation, service.activeAPI = root, generationCtx, 1, api
	done := make(chan struct{})
	go func() {
		service.runGeneration(generationCtx, 1, api)
		close(done)
	}()
	<-api.updatesStarted
	broadcastsBeforeCancel := broadcasts.Load()
	cancelGeneration()
	close(api.releaseUpdates)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("canceled generation did not stop")
	}
	if submits != 0 || len(store.lists[eventsKey]) != 0 || store.values[offsetKey] != "" {
		t.Fatalf("stale generation mutated state: submits=%d events=%d offset=%q", submits, len(store.lists[eventsKey]), store.values[offsetKey])
	}
	if broadcasts.Load() != broadcastsBeforeCancel {
		t.Fatalf("stale generation broadcast after cancellation: before=%d after=%d", broadcastsBeforeCancel, broadcasts.Load())
	}
	if err := service.send(generationCtx, 1, api, "42", "must not send"); !errors.Is(err, context.Canceled) {
		t.Fatalf("stale send error = %v", err)
	}
	api.sendsMu.Lock()
	defer api.sendsMu.Unlock()
	if api.sends != 0 {
		t.Fatalf("stale generation sent %d messages", api.sends)
	}
}

func TestAuthorizationIsChatAndOptionalUser(t *testing.T) {
	cfg := Config{AllowedChatIDs: []string{"-100", "42"}, AllowedUserIDs: []string{"7"}}
	cases := []struct {
		chat, user string
		bot, want  bool
	}{
		{"-100", "7", false, true},
		{"42", "7", false, true},
		{"99", "7", false, false},
		{"-100", "8", false, false},
		{"-100", "7", true, false},
	}
	for _, tc := range cases {
		if got := Authorized(cfg, tc.chat, tc.user, tc.bot); got != tc.want {
			t.Errorf("Authorized(%q,%q,%v)=%v want %v", tc.chat, tc.user, tc.bot, got, tc.want)
		}
	}
	cfg.AllowedUserIDs = nil
	if !Authorized(cfg, "-100", "999", false) {
		t.Fatal("empty user allowlist must admit humans in allowed chat")
	}
}

func TestEventRedactionAndRawRetention(t *testing.T) {
	raw := json.RawMessage(`{"update_id":12,"message":{"text":"denied secret text"}}`)
	event := NewEvent(Update{UpdateID: 12, Raw: raw}, "denied", "FAKE_TOKEN")
	if event.TextPreview != "" || event.RawOmitted {
		t.Fatalf("unexpected denied event: %+v", event)
	}
	encoded, _ := json.Marshal(event)
	if strings.Contains(string(encoded), "FAKE_TOKEN") {
		t.Fatal("active token leaked")
	}
	oversized := json.RawMessage(`{"x":"` + strings.Repeat("a", 17000) + `"}`)
	event = NewEvent(Update{UpdateID: 13, Raw: oversized}, "accepted", "FAKE_TOKEN")
	if !event.RawOmitted || string(event.RawUpdate) != "{}" {
		t.Fatalf("oversized raw was retained: %+v", event)
	}
}

func TestEventRawNumberFidelityAndSanitization(t *testing.T) {
	raw := json.RawMessage(` { "update_id" : 90071992547409931234, "message":{"text":"x"} } `)
	event := NewEventAt(time.UnixMilli(123), Update{UpdateID: 1, Raw: raw}, "denied", "token")
	if string(event.RawUpdate) != `{"update_id":90071992547409931234,"message":{"text":"x"}}` {
		t.Fatalf("raw update changed semantics: %s", event.RawUpdate)
	}
	text := strings.Repeat("a ", 200) + "\x00"
	got := preview(text, 160)
	if len([]rune(got)) != 160 || !strings.HasSuffix(got, "…") {
		t.Fatalf("preview cap = %d, %q", len([]rune(got)), got)
	}
	if got := sanitize("a\nb\tc\x00", 20); got != "a�b�c�" {
		t.Fatalf("label sanitizer = %q", got)
	}
}

func TestUTF16CommandRecognitionAndExactCommands(t *testing.T) {
	if prefix, ok := utf16Prefix("😀/help", 2); !ok || prefix != "😀" {
		t.Fatalf("UTF-16 prefix = %q, %v", prefix, ok)
	}
	message := Message{
		Text: "/HeLp@VM_BOT",
		Entities: []MessageEntity{
			{Type: "bold", Offset: 0, Length: 1},
			{Type: "bot_command", Offset: 0, Length: 12},
		},
	}
	if got := commandName(message, "vm_bot"); got != "help" {
		t.Fatalf("command = %q", got)
	}
	message.Text = "/status argument"
	message.Entities[1].Length = 7
	if got := commandName(message, "vm_bot"); got != "unknown" {
		t.Fatalf("argument command = %q", got)
	}
	for _, command := range []string{"/clear", "/stop", "/unknown"} {
		message.Text = command
		message.Entities[1].Length = len(command)
		if got := commandName(message, "vm_bot"); got != "unknown" {
			t.Fatalf("%s = %q", command, got)
		}
	}
}

func TestBackoffRetryAfterAndNotificationDedup(t *testing.T) {
	if got := backoff(1, 0); got != 800*time.Millisecond {
		t.Fatalf("first backoff = %v", got)
	}
	if got := backoff(8, 1); got != 60*time.Second {
		t.Fatalf("capped backoff = %v", got)
	}
	store := newMemoryStore()
	creator := &fakeNotificationCreator{}
	clock := &fakeClock{now: time.Unix(1000, 0)}
	service := New(Config{Enabled: true, PollTimeoutSeconds: 30, MaxEventLog: 20}, store, nil, creator)
	service.clock = clock
	ctx := context.Background()
	service.suspendedNotification(ctx, false)
	service.suspendedNotification(ctx, false)
	service.deliveryFailed(ctx)
	service.deliveryFailed(ctx)
	creator.mu.Lock()
	if len(creator.requests) != 2 {
		t.Fatalf("deduplicated notifications = %d", len(creator.requests))
	}
	creator.mu.Unlock()
	service.recoveredNotification(ctx)
	creator.mu.Lock()
	if len(creator.requests) != 3 || creator.requests[2].Title != "Telegram polling recovered" {
		t.Fatalf("recovery notifications = %+v", creator.requests)
	}
	creator.mu.Unlock()
}

func TestRetryAfterSuspendsBeforeFirstSuccessfulPoll(t *testing.T) {
	store := newMemoryStore()
	creator := &fakeNotificationCreator{}
	clock := &fakeClock{now: time.Unix(1000, 0)}
	service := New(Config{Enabled: true, PollTimeoutSeconds: 30, MaxEventLog: 20}, store, nil, creator)
	service.clock = clock
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.root, service.activeCtx, service.generation = ctx, ctx, 1
	if !service.retry(ctx, 1, 1, "rate_limited", &APIError{
		Code: "rate_limited", RetryAfter: 2 * time.Minute,
	}, clock.Now()) {
		t.Fatal("retry was canceled")
	}
	service.mu.Lock()
	status := service.status
	service.mu.Unlock()
	if status.State != "polling_suspended" || status.Poll.LastSuccessTs != 0 ||
		status.Poll.RetryAt == nil || *status.Poll.RetryAt != time.Unix(1120, 0).UnixMilli() {
		t.Fatalf("status = %+v", status)
	}
	clock.mu.Lock()
	sleeps := append([]time.Duration(nil), clock.sleeps...)
	clock.mu.Unlock()
	if len(sleeps) != 2 || sleeps[0] != time.Minute || sleeps[1] != time.Minute {
		t.Fatalf("suspension sleeps = %v", sleeps)
	}
	creator.mu.Lock()
	defer creator.mu.Unlock()
	if len(creator.requests) != 1 || creator.requests[0].Title != "Telegram polling suspended" {
		t.Fatalf("notifications = %+v", creator.requests)
	}
}

func TestStatusBroadcastsEveryPublicModelChange(t *testing.T) {
	store := newMemoryStore()
	var mu sync.Mutex
	var statuses []Status
	service := New(Config{AllowedChatIDs: []string{"42"}, MaxEventLog: 20}, store, func(payload []byte) {
		var frame struct {
			Type   string `json:"type"`
			Status Status `json:"status"`
		}
		if json.Unmarshal(payload, &frame) == nil && frame.Type == "telegram-status" {
			mu.Lock()
			statuses = append(statuses, frame.Status)
			mu.Unlock()
		}
	}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.root, service.activeCtx, service.generation = ctx, ctx, 1
	retryAt := time.Unix(200, 0).UnixMilli()
	service.updateStatus(1, func(status *Status) {
		status.Poll.NextOffset = 9
		status.Poll.LastSuccessTs = 100
		status.Poll.ConsecutiveFailures = 2
		status.Poll.RetryAt = &retryAt
	})
	if err := service.observe(ctx, 1, Chat{ID: 42, Type: "private", FirstName: "Mayank"}); err != nil {
		t.Fatal(err)
	}
	if err := service.appendEventFor(ctx, 1, Event{ID: "event-1", Ts: 1, Kind: "system", Outcome: "connected", RawUpdate: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(statuses) != 3 {
		t.Fatalf("status broadcasts = %d, want 3", len(statuses))
	}
	final := statuses[len(statuses)-1]
	if final.Poll.NextOffset != 9 || final.Poll.LastSuccessTs != 100 ||
		final.Poll.ConsecutiveFailures != 2 || final.Poll.RetryAt == nil ||
		len(final.Destinations) != 1 || !final.Destinations[0].Observed || final.EventCount != 1 {
		t.Fatalf("final status = %+v", final)
	}
}

func TestNotificationClaimsSerializeRollbackAndRevisionEligibility(t *testing.T) {
	store := newMemoryStore()
	creator := &fakeNotificationCreator{failures: 1}
	service := New(Config{}, store, nil, creator)
	service.authNotification(context.Background())
	service.authNotification(context.Background())
	creator.mu.Lock()
	if len(creator.requests) != 2 {
		t.Fatalf("failed create did not roll back claim: requests=%d", len(creator.requests))
	}
	creator.mu.Unlock()
	if store.hashes[notificationKey]["auth_active"] != "1" {
		t.Fatal("successful authentication claim was not retained")
	}
	service.clearAuthEligibility()
	service.authNotification(context.Background())
	creator.mu.Lock()
	if len(creator.requests) != 3 {
		t.Fatalf("new secret revision was not eligible: requests=%d", len(creator.requests))
	}
	creator.mu.Unlock()

	concurrentStore := newMemoryStore()
	concurrentCreator := &fakeNotificationCreator{}
	concurrent := New(Config{}, concurrentStore, nil, concurrentCreator)
	var group sync.WaitGroup
	for range 32 {
		group.Add(1)
		go func() {
			defer group.Done()
			concurrent.suspendedNotification(context.Background(), false)
		}()
	}
	group.Wait()
	concurrentCreator.mu.Lock()
	defer concurrentCreator.mu.Unlock()
	if len(concurrentCreator.requests) != 1 {
		t.Fatalf("concurrent claims created %d notifications", len(concurrentCreator.requests))
	}
}

func TestKnownChatLoadDeduplicatesAndFiltersDestinations(t *testing.T) {
	store := newMemoryStore()
	old, _ := json.Marshal(KnownChat{ChatID: "-100", Label: "Old", LastSeenTs: 1})
	newer, _ := json.Marshal(KnownChat{ChatID: "-100", Label: "New", Type: "group", LastSeenTs: 2})
	stale, _ := json.Marshal(KnownChat{ChatID: "999", Label: "Stale", LastSeenTs: 3})
	store.lists[chatsKey] = []string{string(old), string(newer), string(stale)}
	service := New(Config{AllowedChatIDs: []string{"-100", "42"}, MaxEventLog: 20}, store, nil, nil)
	service.loadPersistent()
	var frame struct {
		Status Status `json:"status"`
	}
	if err := json.Unmarshal(service.StatusMessage(), &frame); err != nil {
		t.Fatal(err)
	}
	if len(frame.Status.Destinations) != 2 || frame.Status.Destinations[0].Label != "New" ||
		frame.Status.Destinations[1].Label != "Chat 42" {
		t.Fatalf("destinations = %+v", frame.Status.Destinations)
	}
}
