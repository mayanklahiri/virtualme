package chat

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

	"github.com/mayanklahiri/virtualme/controller/internal/jobs"
	"github.com/mayanklahiri/virtualme/controller/internal/valkey"
)

func TestTelegramMetadataAndLegacyEnvelopeCompatibility(t *testing.T) {
	message := Message{
		ID: "telegram-user:12", Role: "user", Text: "hello", Ts: 1,
		CorrelationID: "telegram:update:12",
		Source:        &Source{Channel: "telegram", ChatID: "-100", UserID: "7", UpdateID: 12},
	}
	raw, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Message
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded.Source == nil || decoded.Source.ChatID != "-100" {
		t.Fatalf("message metadata lost: %+v %v", decoded, err)
	}
	var env jobs.Envelope
	if err := json.Unmarshal([]byte(`{"id":"old","type":"chat","payload":{},"priority":"interactive","enqueuedTs":1,"notBeforeTs":0,"attempts":0,"maxRetries":2,"visibilityTimeoutSec":10,"initiatorConn":"c17","projectId":"","selector":""}`), &env); err != nil {
		t.Fatal(err)
	}
	env.NormalizeLegacy()
	if env.Initiator.ID != "ws:c17" || env.Initiator.ConnectionID != "c17" || !env.Initiator.CancelOnDisconnect {
		t.Fatalf("legacy envelope not normalized: %+v", env)
	}
}

func TestDeliveryRegistrationRejectsDuplicates(t *testing.T) {
	service := New("", "", func([]byte) {})
	service.RegisterDelivery("telegram", func(context.Context, Delivery) error { return nil })
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate delivery registration did not panic")
		}
	}()
	service.RegisterDelivery("telegram", func(context.Context, Delivery) error { return nil })
}

type ingressRESP struct {
	mu       sync.Mutex
	strings  map[string]string
	lists    map[string][]string
	hashes   map[string]map[string]int64
	fail     string
	failMode string
	failed   bool
	addr     string
	close    func()
}

func newIngressRESP(t *testing.T, fail, mode string) *ingressRESP {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &ingressRESP{
		strings: map[string]string{}, lists: map[string][]string{},
		hashes: map[string]map[string]int64{}, fail: fail, failMode: mode,
		addr: listener.Addr().String(),
	}
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

func respBulk(writer io.Writer, value string) {
	fmt.Fprintf(writer, "$%d\r\n%s\r\n", len(value), value)
}

func respArray(writer io.Writer, values ...int64) {
	fmt.Fprintf(writer, "*%d\r\n", len(values))
	for _, value := range values {
		fmt.Fprintf(writer, ":%d\r\n", value)
	}
}

func readIngressCommand(reader *bufio.Reader) ([]string, error) {
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

func trimRange(values []string, start, stop int) []string {
	if start < 0 {
		start += len(values)
	}
	if stop < 0 {
		stop += len(values)
	}
	start, stop = max(0, start), min(len(values)-1, stop)
	if len(values) == 0 || start > stop || start >= len(values) {
		return nil
	}
	return append([]string(nil), values[start:stop+1]...)
}

func (s *ingressRESP) shouldFail(stage string, after bool) bool {
	if s.fail == "" || s.failed || s.fail != stage || (s.failMode == "after") != after {
		return false
	}
	s.failed = true
	return true
}

func (s *ingressRESP) serve(conn net.Conn) {
	defer conn.Close()
	args, err := readIngressCommand(bufio.NewReader(conn))
	if err != nil || len(args) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stage := ""
	switch {
	case args[0] == "EVAL" && strings.Contains(args[1], "ARGV[3] .. oldest"):
		stage = "reserved"
	case args[0] == "EVAL" && strings.Contains(args[1], "record.stage = 'message'"):
		stage = "message"
	case args[0] == "EVAL" && strings.Contains(args[1], "record.stage = 'stats'"):
		stage = "stats"
	case args[0] == "EVAL" && strings.Contains(args[1], "record.stage = 'job'"):
		stage = "job"
	case args[0] == "SET" && strings.Contains(args[2], `"stage":"complete"`):
		stage = "complete"
	}
	if s.shouldFail(stage, false) {
		_, _ = io.WriteString(conn, "-ERR injected before\r\n")
		return
	}
	switch args[0] {
	case "EVAL":
		s.eval(conn, args, stage)
	case "SET":
		s.strings[args[1]] = args[2]
		if s.shouldFail(stage, true) {
			return
		}
		_, _ = io.WriteString(conn, "+OK\r\n")
	case "GET":
		if value, ok := s.strings[args[1]]; ok {
			respBulk(conn, value)
		} else {
			_, _ = io.WriteString(conn, "$-1\r\n")
		}
	case "LLEN":
		fmt.Fprintf(conn, ":%d\r\n", len(s.lists[args[1]]))
	case "LRANGE":
		start, _ := strconv.Atoi(args[2])
		stop, _ := strconv.Atoi(args[3])
		values := trimRange(s.lists[args[1]], start, stop)
		fmt.Fprintf(conn, "*%d\r\n", len(values))
		for _, value := range values {
			respBulk(conn, value)
		}
	case "HGETALL":
		fields := []string{"queries", "promptTokens", "completionTokens", "genMs"}
		fmt.Fprintf(conn, "*%d\r\n", len(fields)*2)
		for _, field := range fields {
			respBulk(conn, field)
			respBulk(conn, strconv.FormatInt(s.hashes[args[1]][field], 10))
		}
	default:
		_, _ = io.WriteString(conn, ":0\r\n")
	}
}

func (s *ingressRESP) eval(conn net.Conn, args []string, stage string) {
	key := args[3]
	switch stage {
	case "reserved":
		if current, ok := s.strings[key]; ok {
			respBulk(conn, current)
			return
		}
		record, updateID, prefix := args[5], args[6], args[7]
		s.strings[key] = record
		indexKey := args[4]
		s.lists[indexKey] = append(s.lists[indexKey], updateID)
		for len(s.lists[indexKey]) > 1000 {
			oldest := s.lists[indexKey][0]
			oldKey := prefix + oldest
			oldRaw, exists := s.strings[oldKey]
			var old telegramIngress
			if exists && json.Unmarshal([]byte(oldRaw), &old) == nil && old.Stage != "complete" {
				break
			}
			s.lists[indexKey] = s.lists[indexKey][1:]
			delete(s.strings, oldKey)
		}
		if s.shouldFail(stage, true) {
			return
		}
		respBulk(conn, record)
	case "message":
		var record telegramIngress
		_ = json.Unmarshal([]byte(s.strings[key]), &record)
		mutated := int64(0)
		if record.Stage == "reserved" {
			s.lists[args[4]] = append(s.lists[args[4]], args[5])
			capacity, _ := strconv.Atoi(args[6])
			if len(s.lists[args[4]]) > capacity {
				s.lists[args[4]] = s.lists[args[4]][len(s.lists[args[4]])-capacity:]
			}
			record.Stage = "message"
			encoded, _ := json.Marshal(record)
			s.strings[key], mutated = string(encoded), 1
		}
		if s.shouldFail(stage, true) {
			return
		}
		fmt.Fprintf(conn, ":%d\r\n", mutated)
	case "stats":
		var record telegramIngress
		_ = json.Unmarshal([]byte(s.strings[key]), &record)
		mutated := int64(0)
		if record.Stage == "message" {
			if s.hashes[args[4]] == nil {
				s.hashes[args[4]] = map[string]int64{}
			}
			s.hashes[args[4]]["queries"]++
			record.Stage = "stats"
			encoded, _ := json.Marshal(record)
			s.strings[key], mutated = string(encoded), 1
		}
		if s.shouldFail(stage, true) {
			return
		}
		fmt.Fprintf(conn, ":%d\r\n", mutated)
	case "job":
		var record telegramIngress
		_ = json.Unmarshal([]byte(s.strings[key]), &record)
		mutated := int64(0)
		if record.Stage == "stats" {
			ahead := len(s.lists[args[4]]) + len(s.lists[args[5]])
			if _, ok := s.strings[args[6]]; ok {
				ahead++
			}
			s.lists[args[4]] = append(s.lists[args[4]], args[7])
			record.Stage, record.Ahead, mutated = "job", ahead, 1
			encoded, _ := json.Marshal(record)
			s.strings[key] = string(encoded)
		}
		if s.shouldFail(stage, true) {
			return
		}
		respArray(conn, mutated, int64(record.Ahead))
	default:
		name := strings.Join(strings.Fields(args[1]), " ")
		if len(name) > 48 {
			name = name[:48]
		}
		fmt.Fprintf(conn, "-ERR unknown script %s\r\n", name)
	}
}

func TestTelegramIngressRepairsEveryCrashWindowExactlyOnce(t *testing.T) {
	for _, stage := range []string{"reserved", "message", "stats", "job", "complete"} {
		for _, mode := range []string{"before", "after"} {
			t.Run(stage+"/"+mode, func(t *testing.T) {
				store := newIngressRESP(t, stage, mode)
				manager := jobs.New(valkey.New(store.addr), func([]byte) {})
				service := New(store.addr, "", func([]byte) {})
				service.SetJobManager(manager)
				submission := Submission{
					Text: "hello", InitiatorID: "tg:-100", CorrelationID: "telegram:update:12",
					Source: Source{Channel: "telegram", ChatID: "-100", UserID: "7", UpdateID: 12},
				}
				if _, err := service.SubmitUserText(context.Background(), submission); err == nil {
					t.Fatalf("first submit should fail at %s/%s", stage, mode)
				}
				result, err := service.SubmitUserText(context.Background(), submission)
				if err != nil {
					t.Fatal(err)
				}
				if result.JobID != "telegram-chat:12" {
					t.Fatalf("result = %+v", result)
				}
				duplicate, err := service.SubmitUserText(context.Background(), submission)
				if err != nil || !duplicate.Duplicate {
					t.Fatalf("completed replay = %+v, %v", duplicate, err)
				}
				store.mu.Lock()
				defer store.mu.Unlock()
				if got := len(store.lists[historyKey]); got != 1 {
					t.Fatalf("history count = %d", got)
				}
				if got := store.hashes[statsKey]["queries"]; got != 1 {
					t.Fatalf("query count = %d", got)
				}
				if got := len(store.lists["virtualme:jobs:ready:interactive"]); got != 1 {
					t.Fatalf("job count = %d", got)
				}
				if got := len(store.lists[telegramIndexKey]); got != 1 {
					t.Fatalf("index count = %d", got)
				}
			})
		}
	}
}

func TestTelegramIngressEvictsOnlyCompleteRecords(t *testing.T) {
	store := newIngressRESP(t, "", "")
	for id := 1; id <= 1000; id++ {
		update := strconv.Itoa(id)
		record, _ := json.Marshal(telegramIngress{UpdateID: int64(id), Stage: "complete"})
		store.strings["virtualme:chat:ingress:telegram:"+update] = string(record)
		store.lists[telegramIndexKey] = append(store.lists[telegramIndexKey], update)
	}
	service := New(store.addr, "", func([]byte) {})
	service.SetJobManager(jobs.New(valkey.New(store.addr), func([]byte) {}))
	_, err := service.SubmitUserText(context.Background(), Submission{
		Text: "new", InitiatorID: "tg:-100", CorrelationID: "telegram:update:1001",
		Source: Source{Channel: "telegram", ChatID: "-100", UserID: "7", UpdateID: 1001},
	})
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.lists[telegramIndexKey]) != 1000 {
		t.Fatalf("index length = %d", len(store.lists[telegramIndexKey]))
	}
	if _, exists := store.strings["virtualme:chat:ingress:telegram:1"]; exists {
		t.Fatal("old complete record was not evicted")
	}
}

func TestHistoryReadyClosesAfterLoad(t *testing.T) {
	store := newIngressRESP(t, "", "")
	service := New(store.addr, "", func([]byte) {})
	go service.LoadHistory()
	select {
	case <-service.HistoryReady():
	case <-time.After(time.Second):
		t.Fatal("history did not become ready")
	}
}
