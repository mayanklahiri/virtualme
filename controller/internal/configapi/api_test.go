package configapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mayanklahiri/virtualme/controller/internal/config"
)

type fakeCoordinator struct {
	mu         sync.Mutex
	preflight  error
	restarts   [][]string
	preflights int
}

type fakePlanner struct {
	calls int
	err   error
}

type blockingCoordinator struct {
	entered chan struct{}
	release chan struct{}
}

func (b *blockingCoordinator) Preflight(context.Context) error {
	close(b.entered)
	<-b.release
	return nil
}

func (b *blockingCoordinator) Restart(context.Context, []string) error { return nil }

func (f *fakePlanner) PlanConfigRestart(context.Context) error {
	f.calls++
	return f.err
}

func (f *fakeCoordinator) Preflight(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.preflights++
	return f.preflight
}

func (f *fakeCoordinator) Restart(_ context.Context, services []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restarts = append(f.restarts, append([]string(nil), services...))
	return nil
}

func TestConfigAPIProjectionSaveConflictAndRestart(t *testing.T) {
	root := t.TempDir()
	loaded, err := config.Load(config.Options{DataDir: root, Env: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	coordinator := new(fakeCoordinator)
	planner := new(fakePlanner)
	var broadcasts [][]byte
	var broadcastMu sync.Mutex
	notices := make(chan SaveNotice, 4)
	shutdowns := make(chan []string, 1)
	service, err := New(Options{
		Loaded: loaded, Environment: []string{}, Coordinator: coordinator, Planner: planner,
		Notifier: ConfigNotifierFunc(func(_ context.Context, notice SaveNotice) error {
			notices <- notice
			return nil
		}),
		Broadcast: func(frame []byte) {
			broadcastMu.Lock()
			broadcasts = append(broadcasts, append([]byte(nil), frame...))
			broadcastMu.Unlock()
		},
		Shutdown: func(services []string) {
			_ = coordinator.Restart(context.Background(), services)
			shutdowns <- services
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	service.Mount(mux)

	response := request(t, mux, http.MethodGet, "/api/config", nil)
	if response.Code != http.StatusOK || bytes.Contains(response.Body.Bytes(), []byte("DO_NOT_LEAK_031")) ||
		bytes.Contains(response.Body.Bytes(), []byte("0001-01-01")) {
		t.Fatalf("bad GET: %d %s", response.Code, response.Body.String())
	}
	var initial map[string]any
	decodeBody(t, response, &initial)
	baseHash := initial["fileHash"].(string)
	raw := cloneRaw(t, initial["raw"])
	raw["llama"].(map[string]any)["contextTokens"] = float64(4096)
	save := request(t, mux, http.MethodPut, "/api/config", map[string]any{
		"baseHash": baseHash, "config": raw,
	})
	if save.Code != http.StatusOK {
		t.Fatalf("save: %d %s", save.Code, save.Body.String())
	}
	var saved map[string]any
	decodeBody(t, save, &saved)
	pendingHash := saved["fileHash"].(string)
	if !saved["pendingRestart"].(bool) || !contains(saved["restartServices"].([]any), "llama") {
		t.Fatalf("bad restart plan: %#v", saved)
	}
	select {
	case notice := <-notices:
		if len(notice.ChangedKeys) != 1 || notice.ChangedKeys[0] != "llama.contextTokens" || !notice.RestartRequired {
			t.Fatalf("bad notice: %#v", notice)
		}
	case <-time.After(time.Second):
		t.Fatal("notifier was not called")
	}
	content, err := os.ReadFile(filepath.Join(root, config.FileName))
	if err != nil || !bytes.Contains(content, []byte("contextTokens: 4096")) {
		t.Fatalf("save was not durable: %v %s", err, content)
	}

	raw["agent"].(map[string]any)["maxSteps"] = float64(99)
	second := request(t, mux, http.MethodPut, "/api/config", map[string]any{
		"baseHash": pendingHash, "config": raw,
	})
	if second.Code != http.StatusOK {
		t.Fatalf("second save: %d %s", second.Code, second.Body.String())
	}
	decodeBody(t, second, &saved)
	pendingHash = saved["fileHash"].(string)
	if !contains(saved["restartServices"].([]any), "llama") ||
		!contains(saved["restartServices"].([]any), "controller") {
		t.Fatalf("cumulative plan lost services: %#v", saved)
	}
	stale := request(t, mux, http.MethodPut, "/api/config", map[string]any{
		"baseHash": baseHash, "config": raw,
	})
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale save status=%d", stale.Code)
	}

	restart := request(t, mux, http.MethodPost, "/api/config/restart", map[string]any{"pendingHash": pendingHash})
	if restart.Code != http.StatusAccepted {
		t.Fatalf("restart: %d %s", restart.Code, restart.Body.String())
	}
	if planner.calls != 1 {
		t.Fatalf("restart planner calls = %d", planner.calls)
	}
	select {
	case services := <-shutdowns:
		if !containsStrings(services, "llama") || services[len(services)-1] != "controller" {
			t.Fatalf("bad shutdown services: %v", services)
		}
	case <-time.After(time.Second):
		t.Fatal("restart shutdown was not scheduled")
	}
	coordinator.mu.Lock()
	if coordinator.preflights != 1 || len(coordinator.restarts) != 1 {
		t.Fatalf("coordinator calls: %#v", coordinator)
	}
	coordinator.mu.Unlock()
	broadcastMu.Lock()
	joined := bytes.Join(broadcasts, []byte("\n"))
	broadcastMu.Unlock()
	if !bytes.Contains(joined, []byte(`"type":"config-saved"`)) ||
		!bytes.Contains(joined, []byte(`"type":"config-restarting"`)) {
		t.Fatalf("missing broadcasts: %s", joined)
	}
}

func TestConfigAPIPreflightFailureAndRequestValidation(t *testing.T) {
	loaded, err := config.Load(config.Options{DataDir: t.TempDir(), Env: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	coordinator := &fakeCoordinator{preflight: errors.New("injected")}
	shutdown := make(chan struct{}, 1)
	var broadcasts int
	service, _ := New(Options{
		Loaded: loaded, Environment: []string{}, Coordinator: coordinator,
		Broadcast: func([]byte) { broadcasts++ },
		Shutdown:  func([]string) { shutdown <- struct{}{} },
	})
	mux := http.NewServeMux()
	service.Mount(mux)

	for _, test := range []struct {
		method, path string
		body         any
		status       int
	}{
		{http.MethodPost, "/api/config/schema", map[string]any{}, http.StatusMethodNotAllowed},
		{http.MethodPut, "/api/config", map[string]any{"unknown": true}, http.StatusBadRequest},
		{http.MethodPost, "/api/config/restart", map[string]any{"pendingHash": "none"}, http.StatusConflict},
		{http.MethodPost, "/api/config/secrets/refresh", map[string]any{"path": "missing"}, http.StatusBadRequest},
	} {
		response := request(t, mux, test.method, test.path, test.body)
		if response.Code != test.status {
			t.Errorf("%s %s status=%d body=%s", test.method, test.path, response.Code, response.Body.String())
		}
	}
	large := httptest.NewRequest(http.MethodPut, "/api/config", strings.NewReader(`{"baseHash":"x","config":{"x":"`+strings.Repeat("a", 1<<20)+`"}}`))
	large.Header.Set("Content-Type", "application/json")
	largeResponse := httptest.NewRecorder()
	mux.ServeHTTP(largeResponse, large)
	if largeResponse.Code != http.StatusBadRequest {
		t.Fatalf("oversized status=%d", largeResponse.Code)
	}

	raw := cloneRaw(t, loaded.Raw)
	raw["agent"].(map[string]any)["maxSteps"] = float64(101)
	save := request(t, mux, http.MethodPut, "/api/config", map[string]any{
		"baseHash": loaded.Hash, "config": raw,
	})
	var saved map[string]any
	decodeBody(t, save, &saved)
	before := broadcasts
	restart := request(t, mux, http.MethodPost, "/api/config/restart", map[string]any{"pendingHash": saved["fileHash"]})
	if restart.Code != http.StatusServiceUnavailable {
		t.Fatalf("preflight status=%d body=%s", restart.Code, restart.Body.String())
	}
	time.Sleep(300 * time.Millisecond)
	select {
	case <-shutdown:
		t.Fatal("shutdown happened after failed preflight")
	default:
	}
	if broadcasts != before {
		t.Fatal("restart broadcast happened after failed preflight")
	}
}

func TestConfigAPISecretRefreshNeverReturnsBytes(t *testing.T) {
	root := t.TempDir()
	secret := filepath.Join(root, "smtp-secret")
	const sentinel = "DO_NOT_LEAK_031"
	if err := os.WriteFile(secret, []byte(sentinel+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	environment := []string{}
	resolver := config.NewResolver(environment)
	defer resolver.Close()
	loaded, err := config.Load(config.Options{DataDir: root, Env: environment, Resolver: resolver})
	if err != nil {
		t.Fatal(err)
	}
	service, _ := New(Options{Loaded: loaded, Environment: environment, Resolver: resolver})
	mux := http.NewServeMux()
	service.Mount(mux)
	raw := cloneRaw(t, loaded.Raw)
	smart := raw["mail"].(map[string]any)["smarthost"].(map[string]any)
	smart["host"], smart["username"], smart["password"] = "smtp.example", "user", "${file:"+secret+"}"
	save := request(t, mux, http.MethodPut, "/api/config", map[string]any{
		"baseHash": loaded.Hash, "config": raw,
	})
	if save.Code != http.StatusOK || strings.Contains(save.Body.String(), sentinel) {
		t.Fatalf("secret save leaked/failed: %d %s", save.Code, save.Body.String())
	}
	refresh := request(t, mux, http.MethodPost, "/api/config/secrets/refresh", map[string]any{
		"path": "mail.smarthost.password",
	})
	if refresh.Code != http.StatusOK || strings.Contains(refresh.Body.String(), sentinel) {
		t.Fatalf("secret refresh leaked/failed: %d %s", refresh.Code, refresh.Body.String())
	}
	get := request(t, mux, http.MethodGet, "/api/config", nil)
	if strings.Contains(get.Body.String(), sentinel) || !strings.Contains(get.Body.String(), `"password":null`) {
		t.Fatalf("secret GET leaked: %s", get.Body.String())
	}
}

func TestRestartSerializesPUTAndRejectsItAfterAcceptance(t *testing.T) {
	loaded, err := config.Load(config.Options{DataDir: t.TempDir(), Env: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	coordinator := &blockingCoordinator{entered: make(chan struct{}), release: make(chan struct{})}
	service, err := New(Options{Loaded: loaded, Environment: []string{}, Coordinator: coordinator})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	service.Mount(mux)
	raw := cloneRaw(t, loaded.Raw)
	raw["agent"].(map[string]any)["maxSteps"] = float64(501)
	save := request(t, mux, http.MethodPut, "/api/config", map[string]any{"baseHash": loaded.Hash, "config": raw})
	var saved map[string]any
	decodeBody(t, save, &saved)
	pendingHash := saved["fileHash"].(string)
	restartDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		restartDone <- request(t, mux, http.MethodPost, "/api/config/restart", map[string]any{"pendingHash": pendingHash})
	}()
	<-coordinator.entered
	raw["agent"].(map[string]any)["maxSteps"] = float64(502)
	putDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		putDone <- request(t, mux, http.MethodPut, "/api/config", map[string]any{"baseHash": pendingHash, "config": raw})
	}()
	select {
	case <-putDone:
		t.Fatal("PUT was not serialized behind restart preflight")
	case <-time.After(50 * time.Millisecond):
	}
	close(coordinator.release)
	if response := <-restartDone; response.Code != http.StatusAccepted {
		t.Fatalf("restart status=%d body=%s", response.Code, response.Body.String())
	}
	if response := <-putDone; response.Code != http.StatusConflict ||
		!strings.Contains(response.Body.String(), "config_restarting") {
		t.Fatalf("PUT after restart status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestRestartRevalidatesFileHashAfterPreflight(t *testing.T) {
	root := t.TempDir()
	loaded, err := config.Load(config.Options{DataDir: root, Env: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	coordinator := &blockingCoordinator{entered: make(chan struct{}), release: make(chan struct{})}
	planner := new(fakePlanner)
	service, _ := New(Options{Loaded: loaded, Environment: []string{}, Coordinator: coordinator, Planner: planner})
	mux := http.NewServeMux()
	service.Mount(mux)
	raw := cloneRaw(t, loaded.Raw)
	raw["agent"].(map[string]any)["maxSteps"] = float64(501)
	save := request(t, mux, http.MethodPut, "/api/config", map[string]any{"baseHash": loaded.Hash, "config": raw})
	var saved map[string]any
	decodeBody(t, save, &saved)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- request(t, mux, http.MethodPost, "/api/config/restart", map[string]any{"pendingHash": saved["fileHash"]})
	}()
	<-coordinator.entered
	if err := os.WriteFile(filepath.Join(root, config.FileName), []byte("# externally changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	close(coordinator.release)
	response := <-done
	if response.Code != http.StatusConflict || planner.calls != 0 {
		t.Fatalf("changed file restart status=%d planner=%d body=%s", response.Code, planner.calls, response.Body.String())
	}
}

func TestConcurrentGETAndSecretRefreshIsRaceFree(t *testing.T) {
	root := t.TempDir()
	secret := filepath.Join(root, "secret")
	if err := os.WriteFile(secret, []byte("value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolver := config.NewResolver(nil)
	defer resolver.Close()
	loaded, err := config.Load(config.Options{DataDir: root, Env: []string{}, Resolver: resolver})
	if err != nil {
		t.Fatal(err)
	}
	service, _ := New(Options{Loaded: loaded, Environment: []string{}, Resolver: resolver})
	mux := http.NewServeMux()
	service.Mount(mux)
	raw := cloneRaw(t, loaded.Raw)
	smart := raw["mail"].(map[string]any)["smarthost"].(map[string]any)
	smart["host"], smart["username"], smart["password"] = "smtp.example", "user", "${file:"+secret+"}"
	save := request(t, mux, http.MethodPut, "/api/config", map[string]any{"baseHash": loaded.Hash, "config": raw})
	if save.Code != http.StatusOK {
		t.Fatalf("save status=%d body=%s", save.Code, save.Body.String())
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if response := request(t, mux, http.MethodGet, "/api/config", nil); response.Code != http.StatusOK {
				t.Errorf("GET status=%d", response.Code)
			}
		}()
		go func() {
			defer wg.Done()
			response := request(t, mux, http.MethodPost, "/api/config/secrets/refresh",
				map[string]any{"path": "mail.smarthost.password"})
			if response.Code != http.StatusOK {
				t.Errorf("refresh status=%d body=%s", response.Code, response.Body.String())
			}
		}()
	}
	wg.Wait()
}

func request(t *testing.T, handler http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var input *bytes.Reader
	if body == nil {
		input = bytes.NewReader(nil)
	} else {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		input = bytes.NewReader(encoded)
	}
	req := httptest.NewRequest(method, path, input)
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func decodeBody(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response: %v: %s", err, response.Body.String())
	}
}

func cloneRaw(t *testing.T, value any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func contains(values []any, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsStrings(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
