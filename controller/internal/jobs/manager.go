// Package jobs implements the reliable Valkey queue and time-bucket scheduler.
package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"strconv"
	"sync"
	"time"

	"github.com/mayanklahiri/virtualme/controller/internal/valkey"
	"github.com/mayanklahiri/virtualme/controller/internal/ws"
)

const (
	readyInteractive = "virtualme:jobs:ready:interactive"
	readyScheduled   = "virtualme:jobs:ready:scheduled"
	inflightKey      = "virtualme:jobs:inflight"
	inflightSinceKey = "virtualme:jobs:inflight-since"
	doneKey          = "virtualme:jobs:done"
	deadKey          = "virtualme:jobs:dead"
)

// Result is the terminal summary persisted with a job.
type Result struct {
	OK         bool   `json:"ok"`
	Summary    string `json:"summary"`
	FinishedTs int64  `json:"finishedTs"`
}

// Envelope is the durable representation of one job.
type Envelope struct {
	ID                   string          `json:"id"`
	Type                 string          `json:"type"`
	Payload              json.RawMessage `json:"payload"`
	Priority             string          `json:"priority"`
	EnqueuedTs           int64           `json:"enqueuedTs"`
	NotBeforeTs          int64           `json:"notBeforeTs"`
	Attempts             int             `json:"attempts"`
	MaxRetries           int             `json:"maxRetries"`
	VisibilityTimeoutSec int             `json:"visibilityTimeoutSec"`
	InitiatorConn        string          `json:"initiatorConn"`
	ProjectID            string          `json:"projectId"`
	Selector             string          `json:"selector"`
	LastError            string          `json:"lastError,omitempty"`
	Result               *Result         `json:"result,omitempty"`
}

type executor func(context.Context, Envelope) (string, error)
type source func(time.Time) []Envelope

type runningJob struct {
	env    Envelope
	cancel context.CancelCauseFunc
}

// Manager owns queue operations, the sequential worker, and the scheduler.
type Manager struct {
	client    *valkey.Client
	broadcast func([]byte)

	mu        sync.Mutex
	executors map[string]executor
	sources   []source
	running   *runningJob

	pollPeriod      time.Duration
	sweepPeriod     time.Duration
	schedulePeriod  time.Duration
	now             func() time.Time
	randomNotBefore func(time.Time, time.Time) time.Time
}

// New creates a queue manager.
func New(client *valkey.Client, broadcast func([]byte)) *Manager {
	return &Manager{
		client: client, broadcast: broadcast, executors: make(map[string]executor),
		pollPeriod: 500 * time.Millisecond, sweepPeriod: 30 * time.Second,
		schedulePeriod: 60 * time.Second, now: time.Now, randomNotBefore: randomInstant,
	}
}

// NewID returns an RFC 4122-shaped random identifier.
func NewID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	text := hex.EncodeToString(value[:])
	return text[:8] + "-" + text[8:12] + "-" + text[12:16] + "-" + text[16:20] + "-" + text[20:]
}

// Register installs an executor for one job type.
func (m *Manager) Register(jobType string, fn func(context.Context, Envelope) (string, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.executors[jobType] = fn
}

// RegisterSource installs a scheduler source.
func (m *Manager) RegisterSource(fn func(time.Time) []Envelope) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sources = append(m.sources, fn)
}

func readyKey(priority string) string {
	if priority == "scheduled" {
		return readyScheduled
	}
	return readyInteractive
}

func normalize(env *Envelope, now time.Time) {
	if env.ID == "" {
		env.ID = NewID()
	}
	if env.Priority != "scheduled" {
		env.Priority = "interactive"
	}
	if env.EnqueuedTs == 0 {
		env.EnqueuedTs = now.UnixMilli()
	}
	if env.MaxRetries == 0 {
		env.MaxRetries = 2
	}
	if env.VisibilityTimeoutSec == 0 {
		env.VisibilityTimeoutSec = 600
	}
	if len(env.Payload) == 0 {
		env.Payload = json.RawMessage(`{}`)
	}
}

// Enqueue persists env and returns how many jobs were already ahead of it.
func (m *Manager) Enqueue(env Envelope) (int, error) {
	normalize(&env, m.now())
	ahead, err := m.depth()
	if err != nil {
		return 0, err
	}
	encoded, err := json.Marshal(env)
	if err != nil {
		return 0, err
	}
	if _, err := m.client.RPush(readyKey(env.Priority), string(encoded)); err != nil {
		return 0, err
	}
	m.broadcastState()
	return ahead, nil
}

func (m *Manager) depth() (int, error) {
	interactive, err := m.client.LLen(readyInteractive)
	if err != nil {
		return 0, err
	}
	scheduled, err := m.client.LLen(readyScheduled)
	if err != nil {
		return 0, err
	}
	m.mu.Lock()
	running := m.running != nil
	m.mu.Unlock()
	total := interactive + scheduled
	if running {
		total++
	}
	return int(total), nil
}

func (m *Manager) acquire() (*Envelope, error) {
	for _, key := range []string{readyInteractive, readyScheduled} {
		raw, err := m.client.LMove(key, inflightKey, "LEFT", "LEFT")
		if err != nil {
			return nil, err
		}
		if raw == nil {
			continue
		}
		var env Envelope
		if err := json.Unmarshal([]byte(*raw), &env); err != nil {
			_, _ = m.client.Del(inflightKey, inflightSinceKey)
			return nil, fmt.Errorf("decode envelope: %w", err)
		}
		if err := m.client.Set(inflightSinceKey, strconv.FormatInt(m.now().UnixMilli(), 10)); err != nil {
			return nil, err
		}
		if m.now().UnixMilli() < env.NotBeforeTs {
			if _, err := m.client.RPush(readyKey(env.Priority), *raw); err != nil {
				return nil, err
			}
			_, _ = m.client.Del(inflightKey, inflightSinceKey)
			m.broadcastState()
			continue
		}
		m.broadcastState()
		return &env, nil
	}
	return nil, nil
}

func (m *Manager) appendFinished(env Envelope, ok bool, summary string) error {
	env.Result = &Result{OK: ok, Summary: summary, FinishedTs: m.now().UnixMilli()}
	encoded, err := json.Marshal(env)
	if err != nil {
		return err
	}
	if _, err := m.client.RPush(doneKey, string(encoded)); err != nil {
		return err
	}
	return m.client.LTrim(doneKey, -200, -1)
}

func (m *Manager) ack(env Envelope, ok bool, summary string) error {
	if err := m.appendFinished(env, ok, summary); err != nil {
		return err
	}
	_, err := m.client.Del(inflightKey, inflightSinceKey)
	m.broadcastState()
	return err
}

func (m *Manager) nack(env Envelope, reason string) error {
	env.Attempts++
	env.LastError = reason
	env.Result = nil
	encoded, err := json.Marshal(env)
	if err != nil {
		return err
	}
	if env.Attempts > env.MaxRetries {
		if _, err := m.client.RPush(deadKey, string(encoded)); err != nil {
			return err
		}
		if err := m.client.LTrim(deadKey, -100, -1); err != nil {
			return err
		}
		if err := m.appendFinished(env, false, reason); err != nil {
			return err
		}
	} else if _, err := m.client.RPush(readyKey(env.Priority), string(encoded)); err != nil {
		return err
	}
	_, err = m.client.Del(inflightKey, inflightSinceKey)
	m.broadcastState()
	return err
}

func (m *Manager) recoverInflight() error {
	items, err := m.client.LRange(inflightKey, 0, 0)
	if err != nil || len(items) == 0 {
		return err
	}
	var env Envelope
	if err := json.Unmarshal([]byte(items[0]), &env); err != nil {
		_, _ = m.client.Del(inflightKey, inflightSinceKey)
		return err
	}
	return m.nack(env, "visibility timeout")
}

func (m *Manager) sweep() {
	m.mu.Lock()
	running := m.running != nil
	m.mu.Unlock()
	if running {
		return
	}
	items, err := m.client.LRange(inflightKey, 0, 0)
	if err != nil || len(items) == 0 {
		return
	}
	sinceText, err := m.client.Get(inflightSinceKey)
	if err != nil || sinceText == nil {
		return
	}
	since, err := strconv.ParseInt(*sinceText, 10, 64)
	if err != nil {
		return
	}
	var env Envelope
	if json.Unmarshal([]byte(items[0]), &env) != nil {
		return
	}
	if m.now().UnixMilli()-since > int64(env.VisibilityTimeoutSec)*1000 {
		if err := m.nack(env, "visibility timeout"); err != nil {
			log.Println("jobs: visibility recovery failed:", err)
		}
	}
}

func (m *Manager) runOne(env Envelope) {
	m.mu.Lock()
	fn := m.executors[env.Type]
	ctx, cancel := context.WithCancelCause(context.Background())
	m.running = &runningJob{env: env, cancel: cancel}
	m.mu.Unlock()
	m.broadcastState()
	var summary string
	var err error
	if fn == nil {
		err = fmt.Errorf("unknown job type %q", env.Type)
	} else {
		summary, err = fn(ctx, env)
	}
	cause := context.Cause(ctx)
	m.mu.Lock()
	m.running = nil
	m.mu.Unlock()
	cancel(nil)
	if cause != nil {
		if err := m.ack(env, false, "cancelled: "+cause.Error()); err != nil {
			log.Println("jobs: cancelled ack failed:", err)
		}
		return
	}
	if err != nil {
		if nackErr := m.nack(env, err.Error()); nackErr != nil {
			log.Println("jobs: nack failed:", nackErr)
		}
		return
	}
	if err := m.ack(env, true, summary); err != nil {
		log.Println("jobs: ack failed:", err)
	}
}

func (m *Manager) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			m.CancelRunning("controller shutdown")
			return
		default:
		}
		env, err := m.acquire()
		if err != nil {
			log.Println("jobs: acquire failed:", err)
		} else if env != nil {
			m.runOne(*env)
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(m.pollPeriod):
		}
	}
}

func randomInstant(start, end time.Time) time.Time {
	remaining := end.Sub(start)
	if remaining <= 0 {
		return start
	}
	value, err := rand.Int(rand.Reader, big.NewInt(remaining.Nanoseconds()))
	if err != nil {
		return start
	}
	return start.Add(time.Duration(value.Int64()))
}

func (m *Manager) schedule() {
	now := m.now()
	m.mu.Lock()
	sources := append([]source(nil), m.sources...)
	m.mu.Unlock()
	for _, provider := range sources {
		for _, env := range provider(now) {
			env.Priority = "scheduled"
			if selector, err := Parse(env.Selector); err == nil {
				_, end := selector.BucketWindow(now)
				if end.Sub(now) >= 5*time.Minute {
					env.NotBeforeTs = m.randomNotBefore(now, end).UnixMilli()
				} else {
					env.NotBeforeTs = now.UnixMilli()
				}
			}
			if _, err := m.Enqueue(env); err != nil {
				log.Println("jobs: scheduler enqueue failed:", err)
			}
		}
	}
}

// Start recovers crash leftovers and starts all manager loops.
func (m *Manager) Start(ctx context.Context) error {
	if err := m.recoverInflight(); err != nil {
		return fmt.Errorf("recover inflight: %w", err)
	}
	go m.worker(ctx)
	go func() {
		<-ctx.Done()
		m.CancelRunning("controller shutdown")
	}()
	go func() {
		sweeper := time.NewTicker(m.sweepPeriod)
		scheduler := time.NewTicker(m.schedulePeriod)
		defer sweeper.Stop()
		defer scheduler.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-sweeper.C:
				m.sweep()
			case <-scheduler.C:
				m.schedule()
			}
		}
	}()
	return nil
}

// CancelRunning cancels the current job, if any.
func (m *Manager) CancelRunning(reason string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running == nil {
		return false
	}
	m.running.cancel(errors.New(reason))
	return true
}

// CancelRunningType cancels the current job only when its type matches.
func (m *Manager) CancelRunningType(jobType, reason string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running == nil || m.running.env.Type != jobType {
		return false
	}
	m.running.cancel(errors.New(reason))
	return true
}

// DropInitiator cancels running and removes queued jobs owned by connID.
func (m *Manager) DropInitiator(connID string) {
	m.mu.Lock()
	if m.running != nil && m.running.env.InitiatorConn == connID {
		m.running.cancel(errors.New("initiator disconnected"))
	}
	m.mu.Unlock()
	for _, key := range []string{readyInteractive, readyScheduled} {
		items, err := m.client.LRange(key, 0, -1)
		if err != nil {
			continue
		}
		for _, item := range items {
			var env Envelope
			if json.Unmarshal([]byte(item), &env) == nil && env.InitiatorConn == connID {
				_, _ = m.client.LRem(key, 0, item)
			}
		}
	}
	m.broadcastState()
}

// DropQueued removes queued jobs of type owned by connID.
func (m *Manager) DropQueued(connID, jobType string) {
	for _, key := range []string{readyInteractive, readyScheduled} {
		items, err := m.client.LRange(key, 0, -1)
		if err != nil {
			continue
		}
		for _, item := range items {
			var env Envelope
			if json.Unmarshal([]byte(item), &env) == nil && env.InitiatorConn == connID && env.Type == jobType {
				_, _ = m.client.LRem(key, 0, item)
			}
		}
	}
	m.broadcastState()
}

func truncatePayload(raw json.RawMessage) any {
	if len(raw) <= 512 {
		var value any
		if json.Unmarshal(raw, &value) == nil {
			return value
		}
		return string(raw)
	}
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return string(raw[:512]) + "…"
	}
	return truncateValue(value)
}

func truncateValue(value any) any {
	switch typed := value.(type) {
	case string:
		if len(typed) > 512 {
			return typed[:512] + "…"
		}
		return typed
	case []any:
		for index, item := range typed {
			typed[index] = truncateValue(item)
		}
	case map[string]any:
		for key, item := range typed {
			typed[key] = truncateValue(item)
		}
	}
	return value
}

func lite(env Envelope) map[string]any {
	encoded, _ := json.Marshal(env)
	var value map[string]any
	_ = json.Unmarshal(encoded, &value)
	value["payload"] = truncatePayload(env.Payload)
	return value
}

// StateMessage returns a bounded queue-state frame.
func (m *Manager) StateMessage() []byte {
	upcoming := make([]map[string]any, 0, 20)
	for _, key := range []string{readyInteractive, readyScheduled} {
		items, _ := m.client.LRange(key, 0, 19)
		for _, item := range items {
			if len(upcoming) == 20 {
				break
			}
			var env Envelope
			if json.Unmarshal([]byte(item), &env) == nil {
				upcoming = append(upcoming, lite(env))
			}
		}
	}
	var running any
	m.mu.Lock()
	if m.running != nil {
		running = lite(m.running.env)
	}
	m.mu.Unlock()
	finished := make([]map[string]any, 0, 20)
	items, _ := m.client.LRange(doneKey, -20, -1)
	for index := len(items) - 1; index >= 0; index-- {
		var env Envelope
		if json.Unmarshal([]byte(items[index]), &env) == nil {
			finished = append(finished, lite(env))
		}
	}
	payload, _ := json.Marshal(map[string]any{
		"type": "queue-state", "upcoming": upcoming, "running": running, "finished": finished,
	})
	return payload
}

func (m *Manager) broadcastState() {
	if m.broadcast != nil {
		m.broadcast(m.StateMessage())
	}
}

// HandleMessage handles the spec's queue websocket messages.
func (m *Manager) HandleMessage(conn *ws.Conn, payload []byte) bool {
	var request struct {
		Type string `json:"type"`
		Job  struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		} `json:"job"`
	}
	if json.Unmarshal(payload, &request) != nil {
		return false
	}
	if request.Type == "queue-peek" {
		_ = conn.WriteText(m.StateMessage())
		return true
	}
	if request.Type != "job-push" {
		return false
	}
	if request.Job.Type != "soak-probe" && request.Job.Type != "manual-tool" {
		_ = conn.WriteText([]byte(`{"type":"chat-error","error":"invalid job type"}`))
		return true
	}
	env := Envelope{
		ID: NewID(), Type: request.Job.Type, Payload: request.Job.Payload,
		Priority: "interactive", InitiatorConn: conn.ID(),
	}
	if _, err := m.Enqueue(env); err != nil {
		message, _ := json.Marshal(map[string]string{"type": "chat-error", "error": "job enqueue failed: " + err.Error()})
		_ = conn.WriteText(message)
		return true
	}
	reply, _ := json.Marshal(map[string]string{"type": "job-pushed", "id": env.ID})
	_ = conn.WriteText(reply)
	return true
}
