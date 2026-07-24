package jiggler

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mayanklahiri/virtualme/controller/internal/actuation"
	"github.com/mayanklahiri/virtualme/controller/internal/jobs"
	"github.com/mayanklahiri/virtualme/controller/internal/valkey"
)

type fakeRunner struct {
	mu    sync.Mutex
	calls [][]string
}

func (runner *fakeRunner) Run(_ context.Context, _ string, args, _ []string, _ string) ([]byte, []byte, error) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.calls = append(runner.calls, append([]string(nil), args...))
	if len(args) > 0 && args[0] == "getmouselocation" {
		return []byte("X=400\nY=300\nSCREEN=0\nWINDOW=1\n"), nil, nil
	}
	return nil, nil, nil
}

func (runner *fakeRunner) count() int {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return len(runner.calls)
}

type fakeJobs struct{ running bool }

func (state *fakeJobs) IsRunning() bool { return state.running }

type fakeActivity struct{ events []jobs.ActivityEvent }

func (activity *fakeActivity) Record(event jobs.ActivityEvent) error {
	activity.events = append(activity.events, event)
	return nil
}

func testService(runner Runner) *Service {
	service := New(runner, nil, nil, 800, 600)
	service.sleep = func(context.Context, time.Duration) bool { return true }
	service.pauseMin, service.pauseMax = 0, 0
	return service
}

func TestBurstDisabledAndYields(t *testing.T) {
	runner := new(fakeRunner)
	service := testService(runner)
	if got := service.burst(context.Background()); got != 0 || runner.count() != 0 {
		t.Fatalf("disabled burst = %d movements, %d calls", got, runner.count())
	}
	service.setCached(true)
	actuation.Lock()
	if got := service.burst(context.Background()); got != 0 {
		t.Fatalf("locked burst = %d movements", got)
	}
	actuation.Unlock()
	if runner.count() != 0 {
		t.Fatalf("locked burst made %d calls", runner.count())
	}
	service.jobs = &fakeJobs{running: true}
	if got := service.burst(context.Background()); got != 0 || runner.count() != 0 {
		t.Fatalf("running-job burst = %d movements, %d calls", got, runner.count())
	}
}

func TestBurstStopsWhenJobStarts(t *testing.T) {
	runner := new(fakeRunner)
	service := testService(runner)
	service.setCached(true)
	state := new(fakeJobs)
	service.jobs = state
	sleeps := 0
	service.sleep = func(context.Context, time.Duration) bool {
		sleeps++
		state.running = true
		return true
	}
	if completed := service.burst(context.Background()); completed != 0 {
		t.Fatalf("completed = %d", completed)
	}
	if sleeps != 1 || runner.count() != 2 {
		t.Fatalf("sleeps = %d, runner calls = %d", sleeps, runner.count())
	}
}

func TestBurstActuatesAndRecordsActivity(t *testing.T) {
	runner := new(fakeRunner)
	service := testService(runner)
	service.setCached(true)
	activity := new(fakeActivity)
	service.SetActivity(activity)
	completed := service.burst(context.Background())
	if completed < 1 || completed > 4 {
		t.Fatalf("completed = %d", completed)
	}
	if runner.count() <= completed {
		t.Fatalf("runner calls = %d", runner.count())
	}
	if len(activity.events) != 1 || activity.events[0].Kind != "tool" ||
		activity.events[0].Name != "jiggle" ||
		activity.events[0].Summary != fmt.Sprintf("jiggler: %d movements", completed) {
		t.Fatalf("activity = %+v", activity.events)
	}
	for _, call := range runner.calls[1:] {
		if len(call) != 3 || call[0] != "mousemove" {
			t.Fatalf("xdotool args = %v", call)
		}
	}
}

type stringRESP struct {
	listener net.Listener
	mu       sync.Mutex
	values   map[string]string
}

func newStringRESP(t *testing.T) *stringRESP {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &stringRESP{listener: listener, values: make(map[string]string)}
	t.Cleanup(func() { _ = listener.Close() })
	go server.run()
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
	args := make([]string, count)
	for index := range args {
		lengthLine, readErr := reader.ReadString('\n')
		if readErr != nil {
			return nil, readErr
		}
		length, parseErr := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(lengthLine, "$")))
		if parseErr != nil {
			return nil, parseErr
		}
		payload := make([]byte, length+2)
		if _, readErr = io.ReadFull(reader, payload); readErr != nil {
			return nil, readErr
		}
		args[index] = string(payload[:length])
	}
	return args, nil
}

func (server *stringRESP) run() {
	for {
		conn, err := server.listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			args, readErr := readCommand(bufio.NewReader(conn))
			if readErr != nil || len(args) < 2 {
				return
			}
			server.mu.Lock()
			defer server.mu.Unlock()
			switch args[0] {
			case "SET":
				server.values[args[1]] = args[2]
				_, _ = io.WriteString(conn, "+OK\r\n")
			case "GET":
				value, ok := server.values[args[1]]
				if !ok {
					_, _ = io.WriteString(conn, "$-1\r\n")
				} else {
					_, _ = fmt.Fprintf(conn, "$%d\r\n%s\r\n", len(value), value)
				}
			}
		}()
	}
}

func TestEnableDisableRoundTripsValkey(t *testing.T) {
	server := newStringRESP(t)
	client := valkey.New(server.listener.Addr().String())
	frames := make([]string, 0, 2)
	service := New(new(fakeRunner), client, func(payload []byte) {
		frames = append(frames, string(payload))
	}, 800, 600)
	if err := service.SetEnabled(true); err != nil || !service.Enabled() {
		t.Fatalf("enable = %v, enabled %v", err, service.Enabled())
	}
	stored, err := client.Get(enabledKey)
	if err != nil || stored == nil || *stored != "1" {
		t.Fatalf("stored enabled = %v, %v", stored, err)
	}
	if err := service.SetEnabled(false); err != nil || service.Enabled() {
		t.Fatalf("disable = %v, enabled %v", err, service.Enabled())
	}
	stored, err = client.Get(enabledKey)
	if err != nil || stored == nil || *stored != "0" {
		t.Fatalf("stored disabled = %v, %v", stored, err)
	}
	if len(frames) != 2 || !strings.Contains(frames[0], `"enabled":true`) ||
		!strings.Contains(frames[1], `"enabled":false`) {
		t.Fatalf("broadcasts = %v", frames)
	}
}
