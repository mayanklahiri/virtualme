package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mayanklahiri/virtualme/controller/internal/valkey"
	"github.com/mayanklahiri/virtualme/controller/internal/ws"
)

const (
	orderKey = "virtualme:notifications:order"
	itemsKey = "virtualme:notifications:items"
	readKey  = "virtualme:notifications:read"
	retain   = 500
)

const createScript = `
local id, body, cap = ARGV[1], ARGV[2], tonumber(ARGV[3])
if redis.call('HEXISTS', KEYS[2], id) == 1 then
  if redis.call('HGET', KEYS[2], id) == body then return 0 end
  return redis.error_reply('ID_CONFLICT')
end
redis.call('HSET', KEYS[2], id, body)
redis.call('LPUSH', KEYS[1], id)
local evicted = redis.call('LRANGE', KEYS[1], cap, -1)
redis.call('LTRIM', KEYS[1], 0, cap - 1)
for _, old in ipairs(evicted) do
  redis.call('HDEL', KEYS[2], old)
  redis.call('HDEL', KEYS[3], old)
end
return 1`

const markOneScript = `
if redis.call('HEXISTS', KEYS[1], ARGV[1]) == 0 then return -1 end
return redis.call('HSETNX', KEYS[2], ARGV[1], ARGV[2])`

const markAllScript = `
local changed = 0
for _, id in ipairs(redis.call('LRANGE', KEYS[1], 0, -1)) do
  if redis.call('HEXISTS', KEYS[2], id) == 1 then
    changed = changed + redis.call('HSETNX', KEYS[3], id, ARGV[1])
  end
end
return changed`

const snapshotScript = `
local result = {}
for _, id in ipairs(redis.call('LRANGE', KEYS[1], 0, -1)) do
  local body = redis.call('HGET', KEYS[2], id)
  if body then
    table.insert(result, body)
    table.insert(result, redis.call('HGET', KEYS[3], id) or '')
  end
end
return result`

// Summary is the bounded list representation sent over websocket.
type Summary struct {
	ID           string `json:"id"`
	Type         string `json:"type"`
	Subtype      string `json:"subtype,omitempty"`
	Sender       string `json:"sender"`
	Title        string `json:"title"`
	Summary      string `json:"summary"`
	OccurredAtMS int64  `json:"occurredAtMs"`
	CreatedAtMS  int64  `json:"createdAtMs"`
	ReadAtMS     int64  `json:"readAtMs,omitempty"`
	Renderer     string `json:"renderer"`
}

// Snapshot is a complete retained server-side snapshot.
type Snapshot struct {
	Notifications []Notification
	Unread        int
}

// Service serializes durable mutations and resulting websocket broadcasts.
type Service struct {
	mu              sync.Mutex
	client          *valkey.Client
	dataDir         string
	broadcast       func([]byte)
	clock           func() time.Time
	ids             *ulidGenerator
	createExactHook func(context.Context, Notification) (Notification, error)
}

// New constructs the singleton notification service.
func New(client *valkey.Client, dataDir string, broadcast func([]byte)) *Service {
	if broadcast == nil {
		broadcast = func([]byte) {}
	}
	return &Service{
		client: client, dataDir: dataDir, broadcast: broadcast,
		clock: time.Now, ids: defaultULIDGenerator(),
	}
}

// Create validates and durably creates one unread notification.
func (s *Service) Create(ctx context.Context, request CreateRequest) (Notification, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	notification, err := validateCreate(request, s.clock())
	if err != nil {
		return Notification{}, err
	}
	if notification.ID == "" {
		notification.ID, err = s.ids.next()
		if err != nil {
			return Notification{}, err
		}
	}
	created, err := s.persistExact(ctx, notification)
	if err != nil {
		return Notification{}, err
	}
	if created {
		if err := s.broadcastStateLocked(&change{Kind: "created", ID: notification.ID}); err != nil {
			log.Printf("notifications: post-create snapshot: %v", err)
		}
	}
	return notification, nil
}

func (s *Service) createExact(ctx context.Context, notification Notification) (Notification, error) {
	if s.createExactHook != nil {
		return s.createExactHook(ctx, notification)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateExact(notification); err != nil {
		return Notification{}, err
	}
	created, err := s.persistExact(ctx, notification)
	if err != nil {
		return Notification{}, err
	}
	if created {
		if err := s.broadcastStateLocked(&change{Kind: "created", ID: notification.ID}); err != nil {
			log.Printf("notifications: post-recovery snapshot: %v", err)
		}
	}
	return notification, nil
}

func validateExact(notification Notification) error {
	if !validULID(notification.ID) || notification.ReadAtMS != 0 || notification.CreatedAtMS <= 0 {
		return errors.New("invalid exact notification")
	}
	validated, err := validateCreate(CreateRequest{
		ID: notification.ID, Type: notification.Type, Subtype: notification.Subtype,
		Sender: notification.Sender, Title: notification.Title, Summary: notification.Summary,
		OccurredAtMS: notification.OccurredAtMS, Renderer: notification.Detail.Renderer,
		Detail: notification.Detail.Data,
	}, time.UnixMilli(notification.CreatedAtMS))
	if err != nil {
		return err
	}
	validated.ID = notification.ID
	expected, _ := json.Marshal(validated)
	actual, _ := json.Marshal(notification)
	if !bytes.Equal(expected, actual) {
		return errors.New("exact notification is not canonical")
	}
	return nil
}

func (s *Service) persistExact(_ context.Context, notification Notification) (bool, error) {
	immutable := notification
	immutable.ReadAtMS = 0
	encoded, err := json.Marshal(immutable)
	if err != nil {
		return false, err
	}
	reply, err := s.client.Eval(createScript, []string{orderKey, itemsKey, readKey},
		notification.ID, string(encoded), strconv.Itoa(retain))
	if err != nil {
		return false, err
	}
	value, err := expectInteger("create", reply)
	return value == 1, err
}

// Snapshot loads all retained notifications newest-first.
func (s *Service) Snapshot() (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *Service) snapshotLocked() (Snapshot, error) {
	reply, err := s.client.Eval(snapshotScript, []string{orderKey, itemsKey, readKey})
	if err != nil {
		return Snapshot{}, err
	}
	items, err := expectArray("snapshot", reply)
	if err != nil || len(items)%2 != 0 {
		return Snapshot{}, fmt.Errorf("notifications: malformed snapshot reply")
	}
	result := Snapshot{Notifications: make([]Notification, 0, len(items)/2)}
	for i := 0; i < len(items); i += 2 {
		body, bodyOK := items[i].(string)
		readAt, readOK := items[i+1].(string)
		if !bodyOK || !readOK {
			return Snapshot{}, errors.New("notifications: malformed snapshot row")
		}
		var notification Notification
		if json.Unmarshal([]byte(body), &notification) != nil || validateExact(notification) != nil {
			log.Printf("notifications: skipping malformed stored row")
			continue
		}
		if readAt != "" {
			value, parseErr := strconv.ParseInt(readAt, 10, 64)
			if parseErr != nil || value <= 0 {
				log.Printf("notifications: skipping malformed read timestamp")
				continue
			}
			notification.ReadAtMS = value
		} else {
			result.Unread++
		}
		result.Notifications = append(result.Notifications, notification)
	}
	return result, nil
}

// Message returns the on-connect authoritative state frame.
func (s *Service) Message() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.messageLocked(nil)
}

type change struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	ReadAtMS int64  `json:"readAtMs"`
}

func stateMessage(notifications []Notification, update *change) ([]byte, error) {
	unread := 0
	for _, item := range notifications {
		if item.ReadAtMS == 0 {
			unread++
		}
	}
	return marshalState(Snapshot{Notifications: notifications, Unread: unread}, update)
}

func (s *Service) messageLocked(update *change) ([]byte, error) {
	snapshot, err := s.snapshotLocked()
	if err != nil {
		return nil, err
	}
	return marshalState(snapshot, update)
}

func marshalState(snapshot Snapshot, update *change) ([]byte, error) {
	count := len(snapshot.Notifications)
	recent := snapshot.Notifications
	if len(recent) > 20 {
		recent = recent[:20]
	}
	summaries := make([]Summary, len(recent))
	for i, item := range recent {
		summaries[i] = summarize(item)
	}
	revision := ""
	if count > 0 {
		revision = snapshot.Notifications[0].ID
	}
	return json.Marshal(struct {
		Type          string           `json:"type"`
		Notifications []Summary        `json:"notifications"`
		Types         []TypeDefinition `json:"types"`
		Unread        int              `json:"unread"`
		RetainedCount int              `json:"retainedCount"`
		Revision      string           `json:"revision"`
		Change        *change          `json:"change,omitempty"`
	}{
		Type: "notifications-state", Notifications: summaries, Types: Registry(),
		Unread: snapshot.Unread, RetainedCount: count, Revision: revision, Change: update,
	})
}

func summarize(item Notification) Summary {
	return Summary{
		ID: item.ID, Type: item.Type, Subtype: item.Subtype, Sender: item.Sender,
		Title: item.Title, Summary: item.Summary, OccurredAtMS: item.OccurredAtMS,
		CreatedAtMS: item.CreatedAtMS, ReadAtMS: item.ReadAtMS, Renderer: item.Detail.Renderer,
	}
}

func (s *Service) broadcastStateLocked(update *change) error {
	payload, err := s.messageLocked(update)
	if err == nil {
		s.broadcast(payload)
	}
	return err
}

// HandleMessage handles notification websocket requests.
func (s *Service) HandleMessage(conn *ws.Conn, payload []byte) bool {
	return s.handleMessage(conn.WriteText, payload)
}

type notificationWriter func([]byte) error

func (s *Service) handleMessage(write notificationWriter, payload []byte) bool {
	var envelope struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(payload, &envelope) != nil || !strings.HasPrefix(envelope.Type, "notification") {
		return false
	}
	switch envelope.Type {
	case "notifications-req":
		var request struct {
			Type string `json:"type"`
		}
		if decodeRequest(payload, &request) != nil {
			s.sendError(write, "", "invalid_request", "invalid notification request")
			return true
		}
		message, err := s.Message()
		if err != nil {
			s.sendError(write, "", "persistence_failed", err.Error())
		} else {
			_ = write(message)
		}
	case "notification-read":
		s.handleRead(write, payload)
	case "notifications-read-all":
		s.handleReadAll(write, payload)
	case "notifications-page-req":
		s.handlePage(write, payload)
	case "notification-detail-req":
		s.handleDetail(write, payload)
	default:
		s.sendError(write, "", "invalid_request", "unknown notification request")
	}
	return true
}

func (s *Service) handleRead(write notificationWriter, payload []byte) {
	var request struct {
		Type      string `json:"type"`
		RequestID string `json:"requestId"`
		ID        string `json:"id"`
	}
	if decodeRequest(payload, &request) != nil || !validRequestID(request.RequestID) || !validULID(request.ID) {
		s.sendError(write, request.RequestID, "invalid_request", "invalid notification read request")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	readAt := s.clock().UnixMilli()
	reply, err := s.client.Eval(markOneScript, []string{itemsKey, readKey}, request.ID, strconv.FormatInt(readAt, 10))
	if err != nil {
		s.sendError(write, request.RequestID, "persistence_failed", err.Error())
		return
	}
	changed, err := expectInteger("mark one", reply)
	if err != nil {
		s.sendError(write, request.RequestID, "persistence_failed", err.Error())
	} else if changed < 0 {
		s.sendError(write, request.RequestID, "not_found", "notification not found")
	} else if changed == 0 {
		s.sendSnapshotLocked(write, request.RequestID)
	} else if err := s.broadcastStateLocked(&change{Kind: "read", ID: request.ID, ReadAtMS: readAt}); err != nil {
		s.sendError(write, request.RequestID, "persistence_failed", err.Error())
	}
}

func (s *Service) handleReadAll(write notificationWriter, payload []byte) {
	var request struct {
		Type      string `json:"type"`
		RequestID string `json:"requestId"`
	}
	if decodeRequest(payload, &request) != nil || !validRequestID(request.RequestID) {
		s.sendError(write, request.RequestID, "invalid_request", "invalid mark-all request")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	readAt := s.clock().UnixMilli()
	reply, err := s.client.Eval(markAllScript, []string{orderKey, itemsKey, readKey}, strconv.FormatInt(readAt, 10))
	if err != nil {
		s.sendError(write, request.RequestID, "persistence_failed", err.Error())
		return
	}
	changed, err := expectInteger("mark all", reply)
	if err != nil {
		s.sendError(write, request.RequestID, "persistence_failed", err.Error())
	} else if changed == 0 {
		s.sendSnapshotLocked(write, request.RequestID)
	} else if err := s.broadcastStateLocked(&change{Kind: "read-all", ReadAtMS: readAt}); err != nil {
		s.sendError(write, request.RequestID, "persistence_failed", err.Error())
	}
}

func (s *Service) sendSnapshotLocked(write notificationWriter, requestID string) {
	message, err := s.messageLocked(nil)
	if err != nil {
		s.sendError(write, requestID, "persistence_failed", err.Error())
	} else {
		_ = write(message)
	}
}

func (s *Service) handlePage(write notificationWriter, payload []byte) {
	var request struct {
		Type      string `json:"type"`
		RequestID string `json:"requestId"`
		Before    string `json:"before"`
		Limit     int    `json:"limit"`
	}
	if decodeRequest(payload, &request) != nil || !validRequestID(request.RequestID) ||
		(request.Before != "" && !validULID(request.Before)) || request.Limit < 1 || request.Limit > 50 {
		s.sendError(write, request.RequestID, "invalid_request", "invalid notification page request")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, err := s.snapshotLocked()
	if err != nil {
		s.sendError(write, request.RequestID, "persistence_failed", err.Error())
		return
	}
	start := 0
	if request.Before != "" {
		start = -1
		for i, item := range snapshot.Notifications {
			if item.ID == request.Before {
				start = i + 1
				break
			}
		}
		if start < 0 {
			s.sendError(write, request.RequestID, "not_found", "notification cursor not found")
			return
		}
	}
	end := min(start+request.Limit, len(snapshot.Notifications))
	summaries := make([]Summary, end-start)
	for i, item := range snapshot.Notifications[start:end] {
		summaries[i] = summarize(item)
	}
	done := end == len(snapshot.Notifications)
	revision := ""
	if len(snapshot.Notifications) > 0 {
		revision = snapshot.Notifications[0].ID
	}
	next := ""
	if !done && len(summaries) > 0 {
		next = summaries[len(summaries)-1].ID
	}
	frame := map[string]any{
		"type": "notifications-page", "requestId": request.RequestID,
		"notifications": summaries, "nextBefore": next, "done": done,
		"retainedCount": len(snapshot.Notifications), "revision": revision,
	}
	encoded, _ := json.Marshal(frame)
	for len(encoded) > 48*1024 && len(summaries) > 1 {
		summaries = summaries[:len(summaries)-1]
		frame["notifications"] = summaries
		frame["done"] = false
		frame["nextBefore"] = summaries[len(summaries)-1].ID
		encoded, _ = json.Marshal(frame)
	}
	_ = write(encoded)
}

func (s *Service) handleDetail(write notificationWriter, payload []byte) {
	var request struct {
		Type      string `json:"type"`
		RequestID string `json:"requestId"`
		ID        string `json:"id"`
	}
	if decodeRequest(payload, &request) != nil || !validRequestID(request.RequestID) || !validULID(request.ID) {
		s.sendError(write, request.RequestID, "invalid_request", "invalid notification detail request")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, err := s.snapshotLocked()
	if err != nil {
		s.sendError(write, request.RequestID, "persistence_failed", err.Error())
		return
	}
	for _, item := range snapshot.Notifications {
		if item.ID == request.ID {
			message, _ := json.Marshal(map[string]any{
				"type": "notification-detail", "requestId": request.RequestID, "notification": item,
			})
			_ = write(message)
			return
		}
	}
	s.sendError(write, request.RequestID, "not_found", "notification not found")
}

func (s *Service) sendError(write notificationWriter, requestID, code, message string) {
	message = sanitizeText(message)
	if runes := []rune(message); len(runes) > 240 {
		message = string(runes[:240])
	}
	frame, _ := json.Marshal(map[string]any{
		"type": "notification-error", "requestId": requestID, "code": code, "error": message,
	})
	_ = write(frame)
}

func decodeRequest(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
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

func expectInteger(name string, reply any) (int64, error) {
	value, ok := reply.(int64)
	if !ok {
		return 0, fmt.Errorf("notifications: %s reply is %T, want integer", name, reply)
	}
	return value, nil
}

func expectArray(name string, reply any) ([]any, error) {
	value, ok := reply.([]any)
	if !ok {
		return nil, fmt.Errorf("notifications: %s reply is %T, want array", name, reply)
	}
	return value, nil
}
