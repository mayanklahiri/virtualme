package configapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/mayanklahiri/virtualme/controller/internal/config"
)

type SaveNotice struct {
	ChangedKeys     []string
	RestartRequired bool
	Revision        string
}

type ConfigNotifier interface {
	Saved(context.Context, SaveNotice) error
}

type ConfigNotifierFunc func(context.Context, SaveNotice) error

func (fn ConfigNotifierFunc) Saved(ctx context.Context, notice SaveNotice) error {
	return fn(ctx, notice)
}

type RestartCoordinator interface {
	Preflight(context.Context) error
	Restart(context.Context, []string) error
}

type RestartPlanner interface {
	PlanConfigRestart(context.Context) error
}

type RestartPlannerFunc func(context.Context) error

func (fn RestartPlannerFunc) PlanConfigRestart(ctx context.Context) error { return fn(ctx) }

type Service struct {
	mu          sync.RWMutex
	schema      *config.Schema
	startup     *config.Loaded
	current     *config.Loaded
	environment []string
	resolver    *config.Resolver
	notifier    ConfigNotifier
	coordinator RestartCoordinator
	planner     RestartPlanner
	broadcast   func([]byte)
	shutdown    func([]string)
}

type Options struct {
	Loaded      *config.Loaded
	Environment []string
	Resolver    *config.Resolver
	Notifier    ConfigNotifier
	Coordinator RestartCoordinator
	Planner     RestartPlanner
	Broadcast   func([]byte)
	Shutdown    func([]string)
}

func New(options Options) (*Service, error) {
	schema, err := config.EmbeddedSchema()
	if err != nil {
		return nil, err
	}
	if options.Loaded == nil {
		return nil, errors.New("loaded config is required")
	}
	if options.Environment == nil {
		options.Environment = os.Environ()
	}
	if options.Resolver == nil {
		options.Resolver = config.NewResolver(options.Environment)
	}
	if options.Notifier == nil {
		options.Notifier = ConfigNotifierFunc(func(context.Context, SaveNotice) error { return nil })
	}
	if options.Broadcast == nil {
		options.Broadcast = func([]byte) {}
	}
	if options.Planner == nil {
		options.Planner = RestartPlannerFunc(func(context.Context) error { return nil })
	}
	if options.Shutdown == nil {
		options.Shutdown = func([]string) {}
	}
	return &Service{
		schema: schema, startup: options.Loaded, current: options.Loaded,
		environment: append([]string(nil), options.Environment...), resolver: options.Resolver,
		notifier: options.Notifier, coordinator: options.Coordinator,
		planner:   options.Planner,
		broadcast: options.Broadcast, shutdown: options.Shutdown,
	}, nil
}

func (s *Service) Mount(mux *http.ServeMux) {
	mux.HandleFunc("/api/config/schema", s.schemaHandler)
	mux.HandleFunc("/api/config", s.configHandler)
	mux.HandleFunc("/api/config/restart", s.restartHandler)
	mux.HandleFunc("/api/config/secrets/refresh", s.refreshHandler)
}

func (s *Service) schemaHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	payload, err := s.schema.UIJSON()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "config_schema", err.Error(), nil)
		return
	}
	writeJSONBytes(w, http.StatusOK, payload)
}

func (s *Service) configHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.get(w)
	case http.MethodPut:
		s.put(w, r)
	default:
		methodNotAllowed(w, http.MethodGet+", "+http.MethodPut)
	}
}

func (s *Service) get(w http.ResponseWriter) {
	s.mu.RLock()
	current := s.current
	startup := s.startup
	services := config.RestartServices(s.schema, startup.Raw, current.Raw)
	response := map[string]any{
		"raw": current.Raw, "effective": config.RedactedEffective(current),
		"sources": current.Sources, "secrets": current.Secrets,
		"fileHash": current.Hash, "startupHash": startup.Hash,
		"pendingRestart": current.Hash != startup.Hash, "restartServices": services,
	}
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, response)
}

func (s *Service) put(w http.ResponseWriter, r *http.Request) {
	var request struct {
		BaseHash string           `json:"baseHash"`
		Config   config.RawConfig `json:"config"`
	}
	if err := decodeStrict(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "config_invalid", err.Error(), nil)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if request.BaseHash != s.current.Hash {
		writeJSON(w, http.StatusConflict, map[string]any{"error": map[string]any{
			"code": "config_conflict", "message": "configuration changed", "currentHash": s.current.Hash,
		}})
		return
	}
	validated, canonical, err := config.ValidateRaw(request.Config, s.current.DataDir, s.environment, s.resolver)
	if err != nil {
		issues := issuesFrom(err)
		writeError(w, http.StatusBadRequest, "config_invalid", config.RedactError(err.Error()).Error(), issues)
		return
	}
	if err := config.AtomicWrite(s.current.File, canonical); err != nil {
		writeError(w, http.StatusInternalServerError, "config_write_failed", err.Error(), nil)
		return
	}
	validated.File = s.current.File
	validated.DataDir = s.current.DataDir
	changed := config.ChangedKeys(s.schema, s.current.Raw, validated.Raw)
	s.current = validated
	services := config.RestartServices(s.schema, s.startup.Raw, validated.Raw)
	payload := map[string]any{
		"ok": true, "fileHash": validated.Hash,
		"pendingRestart": validated.Hash != s.startup.Hash, "restartServices": services,
	}
	writeJSON(w, http.StatusOK, payload)
	frame := cloneMap(payload)
	frame["type"] = "config-saved"
	encoded, _ := json.Marshal(frame)
	s.broadcast(encoded)
	notice := SaveNotice{ChangedKeys: changed, RestartRequired: len(services) > 0, Revision: validated.Hash}
	go func() {
		if err := s.notifier.Saved(context.Background(), notice); err != nil {
			log.Printf("config notifier: %v", config.RedactError(err.Error()))
		}
	}()
}

func (s *Service) refreshHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	var request struct {
		Path string `json:"path"`
	}
	if err := decodeStrict(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "config_invalid", err.Error(), nil)
		return
	}
	s.mu.RLock()
	status, exists := s.current.Secrets[request.Path]
	s.mu.RUnlock()
	if !exists || !status.Configured {
		writeError(w, http.StatusBadRequest, "secret_not_configured", "secret is not configured", nil)
		return
	}
	refreshed, err := s.resolver.Refresh(status.Reference, 5*time.Minute)
	s.mu.Lock()
	s.current.Secrets[request.Path] = refreshed
	s.mu.Unlock()
	if err != nil {
		writeError(w, http.StatusBadGateway, refreshed.Error, "secret refresh failed", nil)
		return
	}
	writeJSON(w, http.StatusOK, refreshed)
}

func (s *Service) restartHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	var request struct {
		PendingHash string `json:"pendingHash"`
	}
	if err := decodeStrict(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "config_invalid", err.Error(), nil)
		return
	}
	s.mu.RLock()
	current := s.current
	startup := s.startup
	services := config.RestartServices(s.schema, startup.Raw, current.Raw)
	pending := current.Hash != startup.Hash && current.Hash == request.PendingHash
	s.mu.RUnlock()
	if !pending {
		writeError(w, http.StatusConflict, "config_conflict", "pending hash changed or no restart is pending", nil)
		return
	}
	if s.coordinator == nil {
		writeError(w, http.StatusServiceUnavailable, "config_preflight_failed", "restart coordinator is unavailable", nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	err := s.coordinator.Preflight(ctx)
	cancel()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "config_preflight_failed", config.RedactError(err.Error()).Error(), nil)
		return
	}
	planCtx, planCancel := context.WithTimeout(r.Context(), 3*time.Second)
	err = s.planner.PlanConfigRestart(planCtx)
	planCancel()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "restart_preparation_failed", config.RedactError(err.Error()).Error(), nil)
		return
	}
	response := map[string]any{"ok": true, "restarting": true, "pendingHash": current.Hash}
	writeJSON(w, http.StatusAccepted, response)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	frame, _ := json.Marshal(map[string]any{
		"type": "config-restarting", "pendingHash": current.Hash, "services": services,
	})
	s.broadcast(frame)
	go func() {
		time.Sleep(250 * time.Millisecond)
		s.shutdown(services)
	}()
}

type CommandCoordinator struct {
	DataDir    string
	ServiceEnv string
	MailDir    string
}

func (c CommandCoordinator) Preflight(ctx context.Context) error {
	command := exec.CommandContext(ctx, "/usr/local/bin/configctl", "preflight",
		"--data-dir", c.DataDir, "--service-env", c.ServiceEnv, "--mail-dir", c.MailDir)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("preflight failed: %s", output)
	}
	return nil
}

var servicePaths = map[string]string{
	"xvfb": "/run/service/svc-xvfb", "openbox": "/run/service/svc-openbox",
	"x11vnc": "/run/service/svc-x11vnc", "novnc": "/run/service/svc-novnc",
	"chromium":          "/run/service/svc-chromium",
	"chromium-watchdog": "/run/service/svc-chromium-watchdog",
	"valkey":            "/run/service/svc-valkey", "llama": "/run/service/svc-llama",
	"ttsd": "/run/service/svc-tts", "mail": "/run/service/svc-mailq",
}

func (c CommandCoordinator) Restart(ctx context.Context, services []string) error {
	for _, service := range services {
		if service == "controller" {
			continue
		}
		servicePath, ok := servicePaths[service]
		if !ok {
			return fmt.Errorf("unknown restart service %q", service)
		}
		if err := exec.CommandContext(ctx, "s6-svc", "-r", servicePath).Run(); err != nil {
			return fmt.Errorf("restart %s: %w", service, err)
		}
	}
	return nil
}

func decodeStrict(w http.ResponseWriter, r *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("request must contain one JSON value")
	}
	return nil
}

func issuesFrom(err error) []config.Issue {
	var validation *config.ValidationError
	if errors.As(err, &validation) {
		return validation.Issues
	}
	var file *config.FileError
	if errors.As(err, &file) {
		return file.Issues
	}
	return nil
}

func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
}

func writeError(w http.ResponseWriter, status int, code, message string, issues []config.Issue) {
	if issues == nil {
		issues = []config.Issue{}
	}
	writeJSON(w, status, map[string]any{"error": map[string]any{
		"code": code, "message": message, "issues": issues,
	}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	payload, _ := json.Marshal(value)
	writeJSONBytes(w, status, payload)
}

func writeJSONBytes(w http.ResponseWriter, status int, payload []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(payload, '\n'))
}

func cloneMap(value map[string]any) map[string]any {
	result := make(map[string]any, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}

func NewProductionCoordinator(dataDir string) CommandCoordinator {
	return CommandCoordinator{
		DataDir: dataDir, ServiceEnv: "/run/virtualme/config.env",
		MailDir: filepath.Join(dataDir, "mail"),
	}
}
