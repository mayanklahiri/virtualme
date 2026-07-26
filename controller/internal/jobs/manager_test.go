package jobs

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

type memoryRESP struct {
	mu      sync.Mutex
	lists   map[string][]string
	strings map[string]string
	close   func()
	addr    string
}

func newMemoryRESP(t *testing.T) *memoryRESP {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &memoryRESP{lists: make(map[string][]string), strings: make(map[string]string), addr: listener.Addr().String()}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go server.serve(conn)
		}
	}()
	server.close = func() {
		_ = listener.Close()
		<-done
	}
	t.Cleanup(server.close)
	return server
}

func readCommand(reader *bufio.Reader) ([]string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	count, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "*")))
	if err != nil {
		return nil, err
	}
	result := make([]string, count)
	for index := range result {
		lengthLine, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		length, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(lengthLine, "$")))
		if err != nil {
			return nil, err
		}
		payload := make([]byte, length+2)
		if _, err := io.ReadFull(reader, payload); err != nil {
			return nil, err
		}
		result[index] = string(payload[:length])
	}
	return result, nil
}

func writeBulk(writer io.Writer, value string) {
	fmt.Fprintf(writer, "$%d\r\n%s\r\n", len(value), value)
}

func normalizeRange(length, start, stop int) (int, int) {
	if start < 0 {
		start = length + start
	}
	if stop < 0 {
		stop = length + stop
	}
	start = max(0, start)
	stop = min(length-1, stop)
	return start, stop
}

func (s *memoryRESP) serve(conn net.Conn) {
	defer conn.Close()
	args, err := readCommand(bufio.NewReader(conn))
	if err != nil || len(args) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch args[0] {
	case "RPUSH":
		s.lists[args[1]] = append(s.lists[args[1]], args[2:]...)
		fmt.Fprintf(conn, ":%d\r\n", len(s.lists[args[1]]))
	case "LPUSH":
		for _, value := range args[2:] {
			s.lists[args[1]] = append([]string{value}, s.lists[args[1]]...)
		}
		fmt.Fprintf(conn, ":%d\r\n", len(s.lists[args[1]]))
	case "LLEN":
		fmt.Fprintf(conn, ":%d\r\n", len(s.lists[args[1]]))
	case "LRANGE":
		start, _ := strconv.Atoi(args[2])
		stop, _ := strconv.Atoi(args[3])
		list := s.lists[args[1]]
		start, stop = normalizeRange(len(list), start, stop)
		if len(list) == 0 || start > stop {
			fmt.Fprint(conn, "*0\r\n")
			return
		}
		fmt.Fprintf(conn, "*%d\r\n", stop-start+1)
		for _, value := range list[start : stop+1] {
			writeBulk(conn, value)
		}
	case "LTRIM":
		start, _ := strconv.Atoi(args[2])
		stop, _ := strconv.Atoi(args[3])
		list := s.lists[args[1]]
		start, stop = normalizeRange(len(list), start, stop)
		if len(list) == 0 || start > stop {
			s.lists[args[1]] = nil
		} else {
			s.lists[args[1]] = append([]string(nil), list[start:stop+1]...)
		}
		fmt.Fprint(conn, "+OK\r\n")
	case "LMOVE":
		src := s.lists[args[1]]
		if len(src) == 0 {
			fmt.Fprint(conn, "$-1\r\n")
			return
		}
		index := 0
		if args[3] == "RIGHT" {
			index = len(src) - 1
		}
		value := src[index]
		s.lists[args[1]] = append(src[:index:index], src[index+1:]...)
		if args[4] == "RIGHT" {
			s.lists[args[2]] = append(s.lists[args[2]], value)
		} else {
			s.lists[args[2]] = append([]string{value}, s.lists[args[2]]...)
		}
		writeBulk(conn, value)
	case "LREM":
		value := args[3]
		var kept []string
		removed := 0
		for _, item := range s.lists[args[1]] {
			if item == value {
				removed++
			} else {
				kept = append(kept, item)
			}
		}
		s.lists[args[1]] = kept
		fmt.Fprintf(conn, ":%d\r\n", removed)
	case "SET":
		s.strings[args[1]] = args[2]
		fmt.Fprint(conn, "+OK\r\n")
	case "GET":
		value, ok := s.strings[args[1]]
		if !ok {
			fmt.Fprint(conn, "$-1\r\n")
		} else {
			writeBulk(conn, value)
		}
	case "DEL":
		removed := 0
		for _, key := range args[1:] {
			if _, ok := s.lists[key]; ok {
				delete(s.lists, key)
				removed++
			}
			if _, ok := s.strings[key]; ok {
				delete(s.strings, key)
				removed++
			}
		}
		fmt.Fprintf(conn, ":%d\r\n", removed)
	default:
		fmt.Fprint(conn, "-ERR unsupported\r\n")
	}
}

func newTestManager(t *testing.T) (*Manager, *memoryRESP) {
	server := newMemoryRESP(t)
	manager := New(valkey.New(server.addr), nil)
	manager.pollPeriod = time.Millisecond
	manager.now = func() time.Time { return time.UnixMilli(1_000_000) }
	return manager, server
}

func decodeAt(t *testing.T, server *memoryRESP, key string, index int) Envelope {
	t.Helper()
	server.mu.Lock()
	defer server.mu.Unlock()
	var env Envelope
	if err := json.Unmarshal([]byte(server.lists[key][index]), &env); err != nil {
		t.Fatal(err)
	}
	return env
}

func TestAcquirePrioritizesInteractive(t *testing.T) {
	manager, _ := newTestManager(t)
	_, _ = manager.Enqueue(Envelope{ID: "scheduled", Type: "x", Priority: "scheduled"})
	_, _ = manager.Enqueue(Envelope{ID: "interactive", Type: "x"})
	first, err := manager.acquire()
	if err != nil || first.ID != "interactive" {
		t.Fatalf("first = %+v, %v", first, err)
	}
	_, _ = manager.client.Del(inflightKey, inflightSinceKey)
	second, err := manager.acquire()
	if err != nil || second.ID != "scheduled" {
		t.Fatalf("second = %+v, %v", second, err)
	}
}

func TestAckRetryAndDeadLetter(t *testing.T) {
	manager, server := newTestManager(t)
	env := Envelope{ID: "job", Type: "x", MaxRetries: 1, Priority: "interactive"}
	_, _ = manager.Enqueue(env)
	acquired, _ := manager.acquire()
	if err := manager.nack(*acquired, "first"); err != nil {
		t.Fatal(err)
	}
	retry := decodeAt(t, server, readyInteractive, 0)
	if retry.Attempts != 1 || retry.LastError != "first" {
		t.Fatalf("retry = %+v", retry)
	}
	acquired, _ = manager.acquire()
	if err := manager.nack(*acquired, "second"); err != nil {
		t.Fatal(err)
	}
	if dead := decodeAt(t, server, deadKey, 0); dead.Attempts != 2 {
		t.Fatalf("dead = %+v", dead)
	}
	if finished := decodeAt(t, server, doneKey, 0); finished.Result == nil || finished.Result.OK {
		t.Fatalf("finished = %+v", finished)
	}
	_, _ = manager.Enqueue(Envelope{ID: "ok", Type: "x"})
	acquired, _ = manager.acquire()
	if err := manager.ack(*acquired, true, "done"); err != nil {
		t.Fatal(err)
	}
	if finished := decodeAt(t, server, doneKey, 1); finished.Result == nil || !finished.Result.OK {
		t.Fatalf("ack = %+v", finished)
	}
}

func TestNotBeforeRequeues(t *testing.T) {
	manager, server := newTestManager(t)
	_, _ = manager.Enqueue(Envelope{ID: "later", Type: "x", NotBeforeTs: 1_001_000})
	got, err := manager.acquire()
	if err != nil || got != nil {
		t.Fatalf("acquire = %+v, %v", got, err)
	}
	if env := decodeAt(t, server, readyInteractive, 0); env.ID != "later" {
		t.Fatalf("requeued = %+v", env)
	}
}

func TestStartupAndSweeperRecovery(t *testing.T) {
	manager, server := newTestManager(t)
	encoded, _ := json.Marshal(Envelope{ID: "crash", Type: "x", Priority: "interactive", MaxRetries: 2, VisibilityTimeoutSec: 1})
	server.lists[inflightKey] = []string{string(encoded)}
	server.strings[inflightSinceKey] = "998000"
	if err := manager.recoverInflight(); err != nil {
		t.Fatal(err)
	}
	if env := decodeAt(t, server, readyInteractive, 0); env.Attempts != 1 {
		t.Fatalf("startup recovery = %+v", env)
	}
	server.lists[readyInteractive] = nil
	server.lists[inflightKey] = []string{string(encoded)}
	server.strings[inflightSinceKey] = "998000"
	manager.sweep()
	if env := decodeAt(t, server, readyInteractive, 0); env.Attempts != 1 {
		t.Fatalf("sweep recovery = %+v", env)
	}
}

func TestDropInitiatorCancelsAndPreservesScheduled(t *testing.T) {
	manager, server := newTestManager(t)
	started := make(chan struct{})
	manager.Register("block", func(ctx context.Context, _ Envelope) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	})
	_, _ = manager.Enqueue(Envelope{ID: "running", Type: "block", InitiatorConn: "c1"})
	_, _ = manager.Enqueue(Envelope{ID: "queued", Type: "block", InitiatorConn: "c1"})
	_, _ = manager.Enqueue(Envelope{ID: "scheduled", Type: "block", Priority: "scheduled"})
	env, _ := manager.acquire()
	go manager.runOne(*env)
	<-started
	manager.DropInitiator("c1")
	deadline := time.Now().Add(time.Second)
	for {
		server.mu.Lock()
		done := len(server.lists[doneKey])
		server.mu.Unlock()
		if done > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cancelled job was not finished")
		}
		time.Sleep(time.Millisecond)
	}
	finished := decodeAt(t, server, doneKey, 0)
	if finished.Result == nil || finished.Result.Summary != "cancelled: initiator disconnected" {
		t.Fatalf("finished = %+v", finished)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if len(server.lists[readyInteractive]) != 0 || len(server.lists[readyScheduled]) != 1 {
		t.Fatalf("ready interactive=%d scheduled=%d", len(server.lists[readyInteractive]), len(server.lists[readyScheduled]))
	}
}

func TestDropConnectionNormalizesLegacyRunningEnvelope(t *testing.T) {
	server := newMemoryRESP(t)
	manager := New(valkey.New(server.addr), func([]byte) {})
	ctx, cancel := context.WithCancelCause(context.Background())
	manager.running = &runningJob{
		env:    Envelope{ID: "legacy", Type: "chat", InitiatorConn: "c1"},
		cancel: cancel,
	}
	manager.DropConnection("c1")
	if cause := context.Cause(ctx); cause == nil || !strings.Contains(cause.Error(), "disconnected") {
		t.Fatalf("legacy running job was not canceled: %v", cause)
	}
	if manager.running.env.Initiator.ID != "ws:c1" || manager.running.env.InitiatorConn != "" {
		t.Fatalf("legacy running envelope not normalized: %+v", manager.running.env)
	}
}

func TestSchedulerPauseSkipsPromotion(t *testing.T) {
	manager, server := newTestManager(t)
	manager.RegisterSource(func(now time.Time) []Envelope {
		return []Envelope{{Type: "x", Selector: "hourly"}}
	})
	if manager.SchedulerPaused() {
		t.Fatal("scheduler must default to running")
	}
	if err := manager.SetSchedulerPaused(true); err != nil {
		t.Fatal(err)
	}
	if !manager.SchedulerPaused() {
		t.Fatal("expected paused after SetSchedulerPaused(true)")
	}
	manager.schedule()
	server.mu.Lock()
	promoted := len(server.lists[readyScheduled])
	server.mu.Unlock()
	if promoted != 0 {
		t.Fatalf("paused scheduler promoted %d jobs", promoted)
	}
	if err := manager.SetSchedulerPaused(false); err != nil {
		t.Fatal(err)
	}
	manager.schedule()
	if env := decodeAt(t, server, readyScheduled, 0); env.Priority != "scheduled" {
		t.Fatalf("resumed scheduler env = %+v", env)
	}
}

func TestSchedulerJitterAndQueueStateTruncation(t *testing.T) {
	manager, server := newTestManager(t)
	now := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }
	manager.randomNotBefore = func(start, end time.Time) time.Time {
		if start != now || end.Hour() != 12 {
			t.Fatalf("jitter bounds %s–%s", start, end)
		}
		return start.Add(time.Minute)
	}
	manager.RegisterSource(func(time.Time) []Envelope {
		return []Envelope{{ID: "scheduled", Type: "x", Selector: "weekday morning", Payload: json.RawMessage(`{"text":"` + strings.Repeat("x", 600) + `"}`)}}
	})
	manager.schedule()
	env := decodeAt(t, server, readyScheduled, 0)
	if env.NotBeforeTs != now.Add(time.Minute).UnixMilli() || env.Priority != "scheduled" {
		t.Fatalf("scheduled = %+v", env)
	}
	message := manager.StateMessage()
	if !strings.Contains(string(message), `"type":"queue-state"`) || strings.Contains(string(message), strings.Repeat("x", 600)) {
		t.Fatalf("queue state = %s", message)
	}
}
