// Package jiggler provides ambient OS-level mouse movement, on by default.
package jiggler

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	mathrand "math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mayanklahiri/virtualme/controller/internal/actuation"
	"github.com/mayanklahiri/virtualme/controller/internal/jobs"
	"github.com/mayanklahiri/virtualme/controller/internal/valkey"
)

const enabledKey = "virtualme:jiggler:enabled"

var errDisabled = errors.New("jiggler disabled")

// Runner executes an xdotool process and returns captured output.
type Runner interface {
	Run(context.Context, string, []string, []string, string) ([]byte, []byte, error)
}

// Service owns jiggler state and its background cadence.
type Service struct {
	runner    Runner
	valkey    *valkey.Client
	broadcast func([]byte)
	width     int
	height    int
	display   string
	xdotool   string

	mu       sync.RWMutex
	enabled  bool
	activity jobs.ActivityRecorder
	rng      *mathrand.Rand
	wake     chan struct{}
	sleep    func(context.Context, time.Duration) bool

	silenceMin time.Duration
	silenceMax time.Duration
	pauseMin   time.Duration
	pauseMax   time.Duration
}

// New constructs a jiggler service (enabled by default once started).
func New(runner Runner, client *valkey.Client, broadcast func([]byte), width, height int) *Service {
	var seedBytes [8]byte
	if _, err := rand.Read(seedBytes[:]); err != nil {
		binary.LittleEndian.PutUint64(seedBytes[:], uint64(time.Now().UnixNano()))
	}
	return &Service{
		runner: runner, valkey: client, broadcast: broadcast, width: width, height: height,
		display: ":99", xdotool: "xdotool",
		rng:  mathrand.New(mathrand.NewSource(int64(binary.LittleEndian.Uint64(seedBytes[:])))),
		wake: make(chan struct{}, 1), sleep: sleepContext,
		silenceMin: 8 * time.Second, silenceMax: 27 * time.Second,
		pauseMin: 300 * time.Millisecond, pauseMax: 1500 * time.Millisecond,
	}
}

// SetDisplay configures the X display used by xdotool.
func (s *Service) SetDisplay(display string) {
	if display != "" {
		s.display = display
	}
}

// SetActivity supplies the durable activity ledger.
func (s *Service) SetActivity(activity jobs.ActivityRecorder) {
	s.activity = activity
}

// Enabled returns the cached persisted setting.
func (s *Service) Enabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.enabled
}

func (s *Service) setCached(enabled bool) {
	s.mu.Lock()
	changed := s.enabled != enabled
	s.enabled = enabled
	s.mu.Unlock()
	if changed && enabled {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
}

// SetEnabled persists and applies the opt-in state.
func (s *Service) SetEnabled(enabled bool) error {
	value := "0"
	if enabled {
		value = "1"
	}
	if err := s.valkey.Set(enabledKey, value); err != nil {
		return err
	}
	s.setCached(enabled)
	if s.broadcast != nil {
		payload, _ := json.Marshal(map[string]any{"type": "jiggler-state", "enabled": enabled})
		s.broadcast(payload)
	}
	return nil
}

// HandleMessage handles a jiggler-set websocket payload.
func (s *Service) HandleMessage(payload []byte) bool {
	var request struct {
		Type    string `json:"type"`
		Enabled *bool  `json:"enabled"`
	}
	if json.Unmarshal(payload, &request) != nil || request.Type != "jiggler-set" {
		return false
	}
	if request.Enabled == nil {
		return true
	}
	if err := s.SetEnabled(*request.Enabled); err != nil {
		log.Println("jiggler: setting state failed:", err)
	}
	return true
}

func sleepContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (s *Service) duration(minimum, maximum time.Duration) time.Duration {
	if maximum <= minimum {
		return minimum
	}
	return minimum + time.Duration(s.rng.Int63n(int64(maximum-minimum)+1))
}

func (s *Service) currentPosition(ctx context.Context) Point {
	stdout, _, err := s.runner.Run(ctx, s.xdotool, []string{"getmouselocation", "--shell"}, []string{"DISPLAY=" + s.display}, "")
	if err != nil {
		return Point{X: s.width / 2, Y: s.height / 2}
	}
	point := Point{X: s.width / 2, Y: s.height / 2}
	for line := range strings.SplitSeq(string(stdout), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		parsed, parseErr := strconv.Atoi(strings.TrimSpace(value))
		if parseErr != nil {
			continue
		}
		switch key {
		case "X":
			point.X = parsed
		case "Y":
			point.Y = parsed
		}
	}
	return clampPoint(point, s.width, s.height)
}

func (s *Service) target() Point {
	if s.rng.Float64() < .15 {
		x := 2 + s.rng.Intn(max(1, s.width-4))
		y := 2 + s.rng.Intn(max(1, s.height-4))
		if s.rng.Intn(2) == 0 {
			x = 2 + s.rng.Intn(max(1, s.width/10))
		} else {
			x = s.width - 3 - s.rng.Intn(max(1, s.width/10))
		}
		if s.rng.Intn(2) == 0 {
			y = 2 + s.rng.Intn(max(1, s.height/10))
		} else {
			y = s.height - 3 - s.rng.Intn(max(1, s.height/10))
		}
		return clampPoint(Point{X: x, Y: y}, s.width, s.height)
	}
	marginX, marginY := s.width/10, s.height/10
	return Point{
		X: marginX + s.rng.Intn(max(1, s.width-2*marginX)),
		Y: marginY + s.rng.Intn(max(1, s.height-2*marginY)),
	}
}

func (s *Service) movementCount() int {
	count := 1
	for count < 4 && s.rng.Float64() >= .45 {
		count++
	}
	return count
}

func (s *Service) move(ctx context.Context, from, to Point) error {
	for _, point := range Trajectory(from, to, s.width, s.height, s.rng) {
		if !s.Enabled() {
			return errDisabled
		}
		stdout, stderr, err := s.runner.Run(ctx, s.xdotool,
			[]string{"mousemove", strconv.Itoa(point.X), strconv.Itoa(point.Y)},
			[]string{"DISPLAY=" + s.display}, "")
		if err != nil {
			return fmt.Errorf("xdotool: %w: %s%s", err, stdout, stderr)
		}
		if !s.sleep(ctx, time.Duration(point.DelayMS)*time.Millisecond) {
			return ctx.Err()
		}
	}
	return nil
}

// burst runs unconditionally while enabled; the only yield is the agent's
// xdotool actuation lock, which protects in-flight agent input.
func (s *Service) burst(ctx context.Context) int {
	if !s.Enabled() || !actuation.TryLock() {
		return 0
	}
	defer actuation.Unlock()
	count := s.movementCount()
	from := s.currentPosition(ctx)
	completed := 0
	for index := range count {
		to := s.target()
		if err := s.move(ctx, from, to); err != nil {
			if ctx.Err() == nil && !errors.Is(err, errDisabled) {
				log.Println("jiggler: movement failed:", err)
			}
			break
		}
		completed++
		from = to
		if index+1 < count && !s.sleep(ctx, s.duration(s.pauseMin, s.pauseMax)) {
			break
		}
	}
	if completed > 0 && s.activity != nil {
		_ = s.activity.Record(jobs.ActivityEvent{
			Kind: "tool", Name: "jiggle",
			Summary: fmt.Sprintf("jiggler: %d movements", completed),
			Detail:  jobs.ActivityDetail{OK: completed == count},
		})
	}
	return completed
}

// Start loads persisted state and runs until ctx is cancelled.
// The jiggler defaults to enabled: only an explicit persisted "0" disables it.
func (s *Service) Start(ctx context.Context) error {
	value, err := s.valkey.Get(enabledKey)
	if err != nil {
		return fmt.Errorf("load enabled state: %w", err)
	}
	s.setCached(value == nil || *value != "0")
	go s.run(ctx)
	return nil
}

func (s *Service) run(ctx context.Context) {
	for {
		wait := s.duration(s.silenceMin, s.silenceMax)
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
			if s.sleep(ctx, 3*time.Second) {
				s.burst(ctx)
			}
		case <-time.After(wait):
			s.burst(ctx)
		}
	}
}
