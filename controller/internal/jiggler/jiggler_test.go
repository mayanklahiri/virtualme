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
	if len(args) > 0 && args[0] == "getdisplaygeometry" {
		return []byte("1280 720\n"), nil, nil
	}
	return nil, nil, nil
}

func (runner *fakeRunner) count() int {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return len(runner.calls)
}

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
	// The agent's actuation lock is the only remaining yield condition.
	actuation.Lock()
	if got := service.burst(context.Background()); got != 0 {
		t.Fatalf("locked burst = %d movements", got)
	}
	actuation.Unlock()
	if runner.count() != 0 {
		t.Fatalf("locked burst made %d calls", runner.count())
	}
}

func TestBurstCadenceIsEightToTwentySevenSeconds(t *testing.T) {
	service := testService(new(fakeRunner))
	if service.silenceMin != 8*time.Second || service.silenceMax != 27*time.Second {
		t.Fatalf("cadence = %v-%v, want 8s-27s", service.silenceMin, service.silenceMax)
	}
	for range 100 {
		wait := service.duration(service.silenceMin, service.silenceMax)
		if wait < 8*time.Second || wait > 27*time.Second {
			t.Fatalf("wait = %v outside 8s-27s", wait)
		}
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

func TestStartDefaultsToEnabled(t *testing.T) {
	server := newStringRESP(t)
	client := valkey.New(server.listener.Addr().String())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Absent key: enabled by default.
	service := New(new(fakeRunner), client, nil, 800, 600)
	service.sleep = func(context.Context, time.Duration) bool { return false }
	if err := service.Start(ctx); err != nil || !service.Enabled() {
		t.Fatalf("fresh start = %v, enabled %v (want enabled)", err, service.Enabled())
	}
	if service.width != 1280 || service.height != 720 {
		t.Fatalf("geometry = %dx%d, want 1280x720 from getdisplaygeometry", service.width, service.height)
	}

	// Only an explicit persisted "0" disables.
	if err := client.Set(enabledKey, "0"); err != nil {
		t.Fatal(err)
	}
	disabled := New(new(fakeRunner), client, nil, 800, 600)
	disabled.sleep = func(context.Context, time.Duration) bool { return false }
	if err := disabled.Start(ctx); err != nil || disabled.Enabled() {
		t.Fatalf("disabled start = %v, enabled %v (want disabled)", err, disabled.Enabled())
	}
}

type geometryRunner struct {
	fakeRunner
	reply string
	fail  bool
}

func (runner *geometryRunner) Run(ctx context.Context, bin string, args, env []string, stdin string) ([]byte, []byte, error) {
	if len(args) > 0 && args[0] == "getdisplaygeometry" {
		runner.mu.Lock()
		runner.calls = append(runner.calls, append([]string(nil), args...))
		runner.mu.Unlock()
		if runner.fail {
			return nil, []byte("err"), fmt.Errorf("no display")
		}
		return []byte(runner.reply), nil, nil
	}
	return runner.fakeRunner.Run(ctx, bin, args, env, stdin)
}

func TestSyncDisplayGeometryOverridesAndFallsBack(t *testing.T) {
	service := testService(&geometryRunner{reply: "1920 1080\n"})
	service.syncDisplayGeometry(context.Background())
	if service.width != 1920 || service.height != 1080 {
		t.Fatalf("got %dx%d", service.width, service.height)
	}

	fallback := testService(&geometryRunner{reply: "garbage\n"})
	fallback.width, fallback.height = 800, 600
	fallback.syncDisplayGeometry(context.Background())
	if fallback.width != 800 || fallback.height != 600 {
		t.Fatalf("garbage reply changed geometry to %dx%d", fallback.width, fallback.height)
	}

	failed := testService(&geometryRunner{fail: true})
	failed.width, failed.height = 800, 600
	failed.syncDisplayGeometry(context.Background())
	if failed.width != 800 || failed.height != 600 {
		t.Fatalf("failed query changed geometry to %dx%d", failed.width, failed.height)
	}
}

func TestMoveReclampsOutsidePoints(t *testing.T) {
	runner := new(fakeRunner)
	service := testService(runner)
	service.setCached(true)
	service.width, service.height = 200, 100
	// Force a trajectory that would otherwise use env-sized coords by using
	// extreme endpoints; clamp must keep every mousemove inside the display.
	if err := service.move(context.Background(), Point{X: 10, Y: 10}, Point{X: 190, Y: 90}); err != nil {
		t.Fatal(err)
	}
	for _, call := range runner.calls {
		if len(call) < 3 || call[0] != "mousemove" {
			continue
		}
		x, _ := strconv.Atoi(call[1])
		y, _ := strconv.Atoi(call[2])
		if x < 2 || x > 197 || y < 2 || y > 97 {
			t.Fatalf("mousemove outside display: %v", call)
		}
	}
}
