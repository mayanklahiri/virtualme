package notifications

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mayanklahiri/virtualme/controller/internal/valkey"
)

type notificationStoreFixture struct {
	t         *testing.T
	listener  net.Listener
	mu        sync.Mutex
	order     []string
	items     map[string]string
	read      map[string]string
	overrides map[string][]string
	commands  [][]string
}

func newNotificationStoreFixture(t *testing.T) *notificationStoreFixture {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fixture := &notificationStoreFixture{
		t: t, listener: listener, items: map[string]string{}, read: map[string]string{},
		overrides: map[string][]string{},
	}
	t.Cleanup(func() { _ = listener.Close() })
	go fixture.serve()
	return fixture
}

func (f *notificationStoreFixture) client() *valkey.Client {
	return valkey.New(f.listener.Addr().String())
}

func (f *notificationStoreFixture) override(script, reply string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.overrides[script] = append(f.overrides[script], reply)
}

func (f *notificationStoreFixture) serve() {
	for {
		conn, err := f.listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			args, readErr := readRESPCommand(bufio.NewReader(conn))
			if readErr != nil {
				f.t.Errorf("read RESP: %v", readErr)
				return
			}
			_, _ = io.WriteString(conn, f.execute(args))
		}()
	}
}

func readRESPCommand(reader *bufio.Reader) ([]string, error) {
	line, err := reader.ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "*") {
		return nil, fmt.Errorf("array header: %q: %w", line, err)
	}
	count, err := strconv.Atoi(strings.TrimSpace(line[1:]))
	if err != nil {
		return nil, err
	}
	result := make([]string, count)
	for index := range result {
		header, headerErr := reader.ReadString('\n')
		if headerErr != nil || !strings.HasPrefix(header, "$") {
			return nil, fmt.Errorf("bulk header: %q: %w", header, headerErr)
		}
		length, parseErr := strconv.Atoi(strings.TrimSpace(header[1:]))
		if parseErr != nil {
			return nil, parseErr
		}
		data := make([]byte, length+2)
		if _, readErr := io.ReadFull(reader, data); readErr != nil {
			return nil, readErr
		}
		result[index] = string(data[:length])
	}
	return result, nil
}

func (f *notificationStoreFixture) execute(command []string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, append([]string(nil), command...))
	if len(command) < 3 || command[0] != "EVAL" {
		return "-ERR unexpected command\r\n"
	}
	script := command[1]
	if queued := f.overrides[script]; len(queued) > 0 {
		f.overrides[script] = queued[1:]
		return queued[0]
	}
	keyCount, err := strconv.Atoi(command[2])
	if err != nil || len(command) < 3+keyCount {
		return "-ERR malformed EVAL\r\n"
	}
	args := command[3+keyCount:]
	switch script {
	case createScript:
		if len(args) != 3 {
			return "-ERR create args\r\n"
		}
		id, body := args[0], args[1]
		if existing, ok := f.items[id]; ok {
			if existing == body {
				return ":0\r\n"
			}
			return "-ID_CONFLICT\r\n"
		}
		f.items[id] = body
		f.order = append([]string{id}, f.order...)
		capacity, _ := strconv.Atoi(args[2])
		for len(f.order) > capacity {
			evicted := f.order[len(f.order)-1]
			f.order = f.order[:len(f.order)-1]
			delete(f.items, evicted)
			delete(f.read, evicted)
		}
		return ":1\r\n"
	case markOneScript:
		if len(args) != 2 {
			return "-ERR mark-one args\r\n"
		}
		if _, ok := f.items[args[0]]; !ok {
			return ":-1\r\n"
		}
		if _, ok := f.read[args[0]]; ok {
			return ":0\r\n"
		}
		f.read[args[0]] = args[1]
		return ":1\r\n"
	case markAllScript:
		changed := 0
		for _, id := range f.order {
			if _, ok := f.items[id]; ok {
				if _, read := f.read[id]; !read {
					f.read[id] = args[0]
					changed++
				}
			}
		}
		return fmt.Sprintf(":%d\r\n", changed)
	case snapshotScript:
		var reply strings.Builder
		rows := make([]string, 0, len(f.order)*2)
		for _, id := range f.order {
			if body, ok := f.items[id]; ok {
				rows = append(rows, body, f.read[id])
			}
		}
		fmt.Fprintf(&reply, "*%d\r\n", len(rows))
		for _, row := range rows {
			fmt.Fprintf(&reply, "$%d\r\n%s\r\n", len(row), row)
		}
		return reply.String()
	default:
		return "-ERR unknown script\r\n"
	}
}

func newFixtureService(t *testing.T, store *notificationStoreFixture, broadcast func([]byte)) *Service {
	t.Helper()
	now := time.UnixMilli(1_700_000_000_000)
	service := New(store.client(), t.TempDir(), broadcast)
	service.clock = func() time.Time { return now }
	service.ids = newULIDGenerator(service.clock, zeroReader{})
	return service
}

func fixtureRequest() CreateRequest {
	return CreateRequest{
		Type: "info", Sender: "agent", Title: "Title", Summary: "Summary",
		Renderer: "agent", Detail: json.RawMessage(`{"value":1}`),
	}
}

func TestStatefulCreateRetentionReadAndSnapshot(t *testing.T) {
	store := newNotificationStoreFixture(t)
	var broadcasts [][]byte
	service := newFixtureService(t, store, func(payload []byte) {
		broadcasts = append(broadcasts, append([]byte(nil), payload...))
	})
	first, err := service.Create(context.Background(), fixtureRequest())
	if err != nil {
		t.Fatal(err)
	}
	if reply, err := store.client().Eval(markOneScript, []string{itemsKey, readKey}, first.ID, "123"); err != nil || reply != int64(1) {
		t.Fatalf("mark first = %#v, %v", reply, err)
	}
	for index := 1; index <= retain; index++ {
		request := fixtureRequest()
		request.Title = fmt.Sprintf("Title %03d", index)
		if _, err := service.Create(context.Background(), request); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Notifications) != retain || snapshot.Unread != retain {
		t.Fatalf("snapshot retained=%d unread=%d", len(snapshot.Notifications), snapshot.Unread)
	}
	store.mu.Lock()
	_, retainedItem := store.items[first.ID]
	_, retainedRead := store.read[first.ID]
	store.mu.Unlock()
	if retainedItem || retainedRead {
		t.Fatal("eviction left item or read hash state")
	}
	if len(broadcasts) != retain+1 {
		t.Fatalf("broadcasts=%d, want %d", len(broadcasts), retain+1)
	}
}

func TestStatefulIdempotenceConflictAndMalformedReplies(t *testing.T) {
	store := newNotificationStoreFixture(t)
	broadcasts := 0
	service := newFixtureService(t, store, func([]byte) { broadcasts++ })
	request := fixtureRequest()
	request.ID = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	if _, err := service.Create(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if broadcasts != 1 {
		t.Fatalf("idempotent create broadcast %d times", broadcasts)
	}
	conflict := request
	conflict.Title = "Different"
	if _, err := service.Create(context.Background(), conflict); err == nil || !strings.Contains(err.Error(), "ID_CONFLICT") {
		t.Fatalf("conflict error=%v", err)
	}
	store.override(snapshotScript, "*1\r\n$3\r\nbad\r\n")
	if _, err := service.Snapshot(); err == nil {
		t.Fatal("accepted odd snapshot reply")
	}
	store.override(snapshotScript, ":1\r\n")
	if _, err := service.Snapshot(); err == nil {
		t.Fatal("accepted non-array snapshot reply")
	}
}

func TestConcurrentCreatesHaveBroadcastOrder(t *testing.T) {
	store := newNotificationStoreFixture(t)
	var mu sync.Mutex
	var broadcastIDs []string
	service := newFixtureService(t, store, func(payload []byte) {
		var frame struct {
			Change *change `json:"change"`
		}
		if json.Unmarshal(payload, &frame) == nil && frame.Change != nil {
			mu.Lock()
			broadcastIDs = append(broadcastIDs, frame.Change.ID)
			mu.Unlock()
		}
	})
	const count = 32
	var wait sync.WaitGroup
	wait.Add(count)
	for index := range count {
		go func() {
			defer wait.Done()
			request := fixtureRequest()
			request.Title = fmt.Sprintf("Concurrent %d", index)
			if _, err := service.Create(context.Background(), request); err != nil {
				t.Errorf("create: %v", err)
			}
		}()
	}
	wait.Wait()
	snapshot, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(broadcastIDs) != count || len(snapshot.Notifications) != count {
		t.Fatalf("broadcasts=%d snapshot=%d", len(broadcastIDs), len(snapshot.Notifications))
	}
	for index, id := range broadcastIDs {
		if got := snapshot.Notifications[count-1-index].ID; got != id {
			t.Fatalf("broadcast[%d]=%s, storage creation order=%s", index, id, got)
		}
	}
}

type frameSink struct {
	mu     sync.Mutex
	frames [][]byte
}

func (s *frameSink) write(payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.frames = append(s.frames, append([]byte(nil), payload...))
	return nil
}

func (s *frameSink) last(t *testing.T) map[string]any {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.frames) == 0 {
		t.Fatal("no frame written")
	}
	var frame map[string]any
	if err := json.Unmarshal(s.frames[len(s.frames)-1], &frame); err != nil {
		t.Fatal(err)
	}
	return frame
}

func TestProtocolRequestsPagesDetailsAndReadSemantics(t *testing.T) {
	store := newNotificationStoreFixture(t)
	var broadcasts frameSink
	service := newFixtureService(t, store, func(payload []byte) { _ = broadcasts.write(payload) })
	first, err := service.Create(context.Background(), fixtureRequest())
	if err != nil {
		t.Fatal(err)
	}
	secondRequest := fixtureRequest()
	secondRequest.Title = "Second"
	second, err := service.Create(context.Background(), secondRequest)
	if err != nil {
		t.Fatal(err)
	}
	var sender frameSink
	if !service.handleMessage(sender.write, []byte(`{"type":"notifications-page-req","requestId":"p1","before":"","limit":1}`)) {
		t.Fatal("page request was not handled")
	}
	page := sender.last(t)
	rows := page["notifications"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["id"] != second.ID ||
		page["nextBefore"] != second.ID || page["done"] != false {
		t.Fatalf("page=%#v", page)
	}
	service.handleMessage(sender.write, []byte(fmt.Sprintf(
		`{"type":"notifications-page-req","requestId":"p2","before":%q,"limit":50}`, second.ID)))
	page = sender.last(t)
	rows = page["notifications"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["id"] != first.ID || page["done"] != true {
		t.Fatalf("second page=%#v", page)
	}
	service.handleMessage(sender.write, []byte(fmt.Sprintf(
		`{"type":"notification-detail-req","requestId":"d1","id":%q}`, first.ID)))
	if detail := sender.last(t); detail["type"] != "notification-detail" ||
		detail["notification"].(map[string]any)["id"] != first.ID {
		t.Fatalf("detail=%#v", detail)
	}
	service.handleMessage(sender.write, []byte(fmt.Sprintf(
		`{"type":"notification-read","requestId":"r1","id":%q}`, first.ID)))
	if update := broadcasts.last(t)["change"].(map[string]any); update["kind"] != "read" || update["id"] != first.ID {
		t.Fatalf("read update=%#v", update)
	}
	firstRead := store.read[first.ID]
	service.handleMessage(sender.write, []byte(fmt.Sprintf(
		`{"type":"notification-read","requestId":"r2","id":%q}`, first.ID)))
	if store.read[first.ID] != firstRead || sender.last(t)["change"] != nil {
		t.Fatal("idempotent read changed timestamp or globally broadcast")
	}
	service.handleMessage(sender.write, []byte(`{"type":"notifications-read-all","requestId":"a1"}`))
	if update := broadcasts.last(t)["change"].(map[string]any); update["kind"] != "read-all" {
		t.Fatalf("read-all update=%#v", update)
	}
	thirdRequest := fixtureRequest()
	thirdRequest.Title = "After cutoff"
	third, err := service.Create(context.Background(), thirdRequest)
	if err != nil {
		t.Fatal(err)
	}
	if store.read[third.ID] != "" {
		t.Fatal("create after mark-all cutoff was read")
	}
	service.handleMessage(sender.write, []byte(`{"type":"notification-read","requestId":"bad","id":"bad"}`))
	if frame := sender.last(t); frame["code"] != "invalid_request" || frame["requestId"] != "bad" {
		t.Fatalf("invalid response=%#v", frame)
	}
	service.handleMessage(sender.write, []byte(
		`{"type":"notifications-page-req","requestId":"p3","before":"01ARZ3NDEKTSV4RRFFQ69G5FAV","limit":50}`))
	if frame := sender.last(t); frame["code"] != "not_found" {
		t.Fatalf("cursor response=%#v", frame)
	}
	service.handleMessage(sender.write, []byte(`{"type":"notifications-req","extra":true}`))
	if frame := sender.last(t); frame["code"] != "invalid_request" {
		t.Fatalf("unknown field response=%#v", frame)
	}
}

func TestPersistenceFailureDoesNotBroadcast(t *testing.T) {
	store := newNotificationStoreFixture(t)
	broadcasts := 0
	service := newFixtureService(t, store, func([]byte) { broadcasts++ })
	store.override(createScript, "-ERR injected\r\n")
	if _, err := service.Create(context.Background(), fixtureRequest()); err == nil {
		t.Fatal("create unexpectedly succeeded")
	}
	if broadcasts != 0 {
		t.Fatalf("failed create broadcast %d frames", broadcasts)
	}
	store.override(createScript, "+OK\r\n")
	if _, err := service.Create(context.Background(), fixtureRequest()); err == nil ||
		!strings.Contains(err.Error(), "want integer") {
		t.Fatalf("malformed create reply error=%v", err)
	}
}

func TestSnapshotSkipsMalformedRowsAndPageFrameIsBounded(t *testing.T) {
	store := newNotificationStoreFixture(t)
	service := newFixtureService(t, store, nil)
	store.mu.Lock()
	store.order = append(store.order, "01ARZ3NDEKTSV4RRFFQ69G5FAV")
	store.items["01ARZ3NDEKTSV4RRFFQ69G5FAV"] = `{"untrusted":true}`
	store.mu.Unlock()
	snapshot, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Notifications) != 0 || snapshot.Unread != 0 {
		t.Fatalf("malformed row leaked: %#v", snapshot)
	}
	store.mu.Lock()
	store.order = nil
	store.items = map[string]string{}
	store.mu.Unlock()
	for index := 0; index < 50; index++ {
		request := fixtureRequest()
		request.Title = strings.Repeat("界", 120)
		request.Summary = strings.Repeat("界", 240)
		request.Subtype = fmt.Sprintf("large-%d", index)
		if _, err := service.Create(context.Background(), request); err != nil {
			t.Fatal(err)
		}
	}
	var sender frameSink
	service.handleMessage(sender.write, []byte(`{"type":"notifications-page-req","requestId":"large","before":"","limit":50}`))
	sender.mu.Lock()
	payload := sender.frames[len(sender.frames)-1]
	sender.mu.Unlock()
	if len(payload) > 48*1024 {
		t.Fatalf("page frame is %d bytes", len(payload))
	}
	var page struct {
		Notifications []Summary `json:"notifications"`
		Done          bool      `json:"done"`
		NextBefore    string    `json:"nextBefore"`
	}
	if err := json.Unmarshal(payload, &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Notifications) < 1 || len(page.Notifications) >= 50 || page.Done || page.NextBefore == "" {
		t.Fatalf("bounded page=%#v", page)
	}
}
