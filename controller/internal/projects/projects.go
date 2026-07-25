// Package projects implements persistent recurring natural-language tasks.
package projects

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mayanklahiri/virtualme/controller/internal/jobs"
	"github.com/mayanklahiri/virtualme/controller/internal/valkey"
	"github.com/mayanklahiri/virtualme/controller/internal/ws"
)

const projectsKey = "virtualme:projects"

// Project is the durable project record.
type Project struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Task               string `json:"task"`
	Selector           string `json:"selector"`
	Enabled            bool   `json:"enabled"`
	CreatedTs          int64  `json:"createdTs"`
	LastRunTs          int64  `json:"lastRunTs"`
	LastEnqueuedBucket string `json:"lastEnqueuedBucket"`
}

// RunSummary is a bounded record of one execution attempt.
type RunSummary struct {
	Ts         int64  `json:"ts"`
	JobID      string `json:"jobId"`
	OK         bool   `json:"ok"`
	Summary    string `json:"summary"`
	DurationMs int64  `json:"durationMs"`
	Manual     bool   `json:"manual"`
}

// TaskRunner executes a project without modifying chat history.
type TaskRunner interface {
	RunTask(context.Context, string) (string, error)
}

// Service owns project persistence, scheduling, execution, and websocket CRUD.
type Service struct {
	client    *valkey.Client
	jobs      *jobs.Manager
	runner    TaskRunner
	dataDir   string
	broadcast func([]byte)
	now       func() time.Time
}

// New creates and registers a project service.
func New(client *valkey.Client, manager *jobs.Manager, runner TaskRunner, dataDir string, broadcast func([]byte)) *Service {
	service := &Service{
		client: client, jobs: manager, runner: runner, dataDir: dataDir,
		broadcast: broadcast, now: time.Now,
	}
	manager.Register("project-run", service.Execute)
	manager.RegisterSource(service.Source)
	return service
}

func runsKey(id string) string {
	return "virtualme:projects:runs:" + id
}

func (s *Service) list() ([]Project, error) {
	values, err := s.client.HGetAll(projectsKey)
	if err != nil {
		return nil, err
	}
	projects := make([]Project, 0, len(values))
	for _, value := range values {
		var project Project
		if json.Unmarshal([]byte(value), &project) == nil {
			projects = append(projects, project)
		}
	}
	sort.Slice(projects, func(i, j int) bool {
		left, right := strings.ToLower(projects[i].Name), strings.ToLower(projects[j].Name)
		if left == right {
			return projects[i].ID < projects[j].ID
		}
		return left < right
	})
	return projects, nil
}

func (s *Service) get(id string) (Project, error) {
	value, err := s.client.HGet(projectsKey, id)
	if err != nil {
		return Project{}, err
	}
	if value == nil {
		return Project{}, fmt.Errorf("project not found")
	}
	var project Project
	if err := json.Unmarshal([]byte(*value), &project); err != nil {
		return Project{}, fmt.Errorf("decode project: %w", err)
	}
	return project, nil
}

func (s *Service) save(project Project) error {
	encoded, err := json.Marshal(project)
	if err != nil {
		return err
	}
	_, err = s.client.HSet(projectsKey, project.ID, string(encoded))
	return err
}

func validateName(value string) (string, error) {
	value = strings.TrimSpace(value)
	count := utf8.RuneCountInString(value)
	if count < 1 || count > 80 {
		return "", fmt.Errorf("name must be 1-80 characters")
	}
	return value, nil
}

func validateTask(value string) error {
	if utf8.RuneCountInString(value) > 4096 {
		return fmt.Errorf("task must be at most 4096 characters")
	}
	return nil
}

func (s *Service) create(name string) (Project, error) {
	name, err := validateName(name)
	if err != nil {
		return Project{}, err
	}
	project := Project{
		ID: jobs.NewID(), Name: name, Selector: "weekday morning",
		Enabled: false, CreatedTs: s.now().UnixMilli(),
	}
	return project, s.save(project)
}

type updateRequest struct {
	ID       string  `json:"id"`
	Name     *string `json:"name"`
	Task     *string `json:"task"`
	Selector *string `json:"selector"`
	Enabled  *bool   `json:"enabled"`
}

func (s *Service) update(request updateRequest) (Project, error) {
	project, err := s.get(request.ID)
	if err != nil {
		return Project{}, err
	}
	if request.Name != nil {
		project.Name, err = validateName(*request.Name)
		if err != nil {
			return Project{}, err
		}
	}
	if request.Task != nil {
		if err := validateTask(*request.Task); err != nil {
			return Project{}, err
		}
		project.Task = *request.Task
	}
	if request.Selector != nil {
		selector := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(*request.Selector)), " "))
		if _, err := jobs.Parse(selector); err != nil {
			return Project{}, fmt.Errorf("invalid selector: %w", err)
		}
		project.Selector = strings.ReplaceAll(selector, ", ", ",")
	}
	if request.Enabled != nil {
		project.Enabled = *request.Enabled
	}
	return project, s.save(project)
}

func (s *Service) readRuns(id string, limit int) []RunSummary {
	values, err := s.client.LRange(runsKey(id), 0, limit-1)
	if err != nil {
		return []RunSummary{}
	}
	runs := make([]RunSummary, 0, len(values))
	for _, value := range values {
		var run RunSummary
		if json.Unmarshal([]byte(value), &run) == nil {
			runs = append(runs, run)
		}
	}
	return runs
}

// Message returns the complete projects frame with five recent runs each.
func (s *Service) Message() []byte {
	projects, err := s.list()
	if err != nil {
		projects = []Project{}
	}
	runs := make(map[string][]RunSummary, len(projects))
	for _, project := range projects {
		runs[project.ID] = s.readRuns(project.ID, 5)
	}
	payload, _ := json.Marshal(map[string]any{"type": "projects", "projects": projects, "runs": runs})
	return payload
}

func (s *Service) publish() {
	if s.broadcast != nil {
		s.broadcast(s.Message())
	}
}

func writeError(conn *ws.Conn, err error) {
	payload, _ := json.Marshal(map[string]string{"type": "project-error", "error": err.Error()})
	_ = conn.WriteText(payload)
}

// HandleMessage handles project websocket requests.
func (s *Service) HandleMessage(conn *ws.Conn, payload []byte) bool {
	var request struct {
		Type     string  `json:"type"`
		ID       string  `json:"id"`
		Name     string  `json:"name"`
		Task     *string `json:"task"`
		Selector *string `json:"selector"`
		Enabled  *bool   `json:"enabled"`
	}
	if json.Unmarshal(payload, &request) != nil || !strings.HasPrefix(request.Type, "project") {
		return false
	}
	switch request.Type {
	case "projects-req":
		_ = conn.WriteText(s.Message())
	case "project-create":
		if _, err := s.create(request.Name); err != nil {
			writeError(conn, err)
			return true
		}
		s.publish()
	case "project-update":
		var name *string
		if raw := map[string]json.RawMessage{}; json.Unmarshal(payload, &raw) == nil {
			if _, present := raw["name"]; present {
				name = &request.Name
			}
		}
		if _, err := s.update(updateRequest{
			ID: request.ID, Name: name, Task: request.Task,
			Selector: request.Selector, Enabled: request.Enabled,
		}); err != nil {
			writeError(conn, err)
			return true
		}
		s.publish()
	case "project-delete":
		if _, err := s.get(request.ID); err != nil {
			writeError(conn, err)
			return true
		}
		if _, err := s.client.HDel(projectsKey, request.ID); err != nil {
			writeError(conn, err)
			return true
		}
		if _, err := s.client.Del(runsKey(request.ID)); err != nil {
			writeError(conn, err)
			return true
		}
		s.publish()
	case "project-run":
		project, err := s.get(request.ID)
		if err != nil {
			writeError(conn, err)
			return true
		}
		body, _ := json.Marshal(map[string]any{"id": request.ID, "name": project.Name, "manual": true})
		if _, err := s.jobs.Enqueue(jobs.Envelope{
			ID: jobs.NewID(), Type: "project-run", Payload: body,
			Priority:  "interactive",
			Initiator: jobs.Initiator{ID: "ws:" + conn.ID(), Kind: "web", ConnectionID: conn.ID(), CancelOnDisconnect: true},
			ProjectID: request.ID, VisibilityTimeoutSec: 1800, MaxRetries: 1,
		}); err != nil {
			writeError(conn, fmt.Errorf("project enqueue failed: %w", err))
		}
	default:
		return false
	}
	return true
}

func bucketToken(now time.Time, bucket string) string {
	return now.Format("2006-01-02") + "/" + bucket
}

// Source produces due scheduled project envelopes and persists dedup tokens.
func (s *Service) Source(now time.Time) []jobs.Envelope {
	projects, err := s.list()
	if err != nil {
		return nil
	}
	var result []jobs.Envelope
	for _, project := range projects {
		selector, err := jobs.Parse(project.Selector)
		if err != nil || !project.Enabled || !selector.Matches(now) {
			continue
		}
		token := bucketToken(now, selector.Bucket)
		if project.LastEnqueuedBucket == token {
			continue
		}
		project.LastEnqueuedBucket = token
		if s.save(project) != nil {
			continue
		}
		body, _ := json.Marshal(map[string]any{"id": project.ID, "name": project.Name, "manual": false})
		result = append(result, jobs.Envelope{
			ID: jobs.NewID(), Type: "project-run", Payload: body,
			Priority: "scheduled", ProjectID: project.ID, Selector: project.Selector,
			Initiator:            jobs.Initiator{ID: "system:projects", Kind: "system"},
			VisibilityTimeoutSec: 1800, MaxRetries: 1,
		})
	}
	if len(result) > 0 {
		s.publish()
	}
	return result
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return value
}

// Execute runs one project envelope and records its outcome.
func (s *Service) Execute(ctx context.Context, env jobs.Envelope) (reply string, runErr error) {
	started := s.now()
	var payload struct {
		ID     string `json:"id"`
		Manual bool   `json:"manual"`
	}
	if err := json.Unmarshal(env.Payload, &payload); err != nil || payload.ID == "" {
		return "", fmt.Errorf("invalid project payload")
	}
	project, err := s.get(payload.ID)
	if err != nil {
		return "", err
	}
	scratch := filepath.Join(s.dataDir, "projects", project.ID)
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return "", fmt.Errorf("create project scratch directory: %w", err)
	}
	prompt := fmt.Sprintf("Project %q scratch directory: %s. You may read and write files there with the bash tool.\n%s", project.Name, scratch, project.Task)
	reply, runErr = s.runner.RunTask(ctx, prompt)
	summary := reply
	if runErr != nil {
		summary = runErr.Error()
	}
	finished := s.now()
	run := RunSummary{
		Ts: finished.UnixMilli(), JobID: env.ID, OK: runErr == nil,
		Summary: truncate(summary, 300), DurationMs: finished.Sub(started).Milliseconds(),
		Manual: payload.Manual,
	}
	encoded, _ := json.Marshal(run)
	if _, err := s.client.LPush(runsKey(project.ID), string(encoded)); err != nil && runErr == nil {
		runErr = err
	}
	if err := s.client.LTrim(runsKey(project.ID), 0, 49); err != nil && runErr == nil {
		runErr = err
	}
	project.LastRunTs = finished.UnixMilli()
	if err := s.save(project); err != nil && runErr == nil {
		runErr = err
	}
	s.publish()
	return truncate(reply, 300), runErr
}
