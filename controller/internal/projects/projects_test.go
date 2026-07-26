package projects

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mayanklahiri/virtualme/controller/internal/jobs"
	"github.com/mayanklahiri/virtualme/controller/internal/valkey"
)

type memoryValkey struct {
	mu     sync.Mutex
	hashes map[string]map[string]string
	lists  map[string][]string
	addr   string
}

func newMemoryValkey(t *testing.T) *memoryValkey {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &memoryValkey{
		hashes: make(map[string]map[string]string),
		lists:  make(map[string][]string),
		addr:   listener.Addr().String(),
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
	t.Cleanup(func() {
		_ = listener.Close()
		<-done
	})
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
		value := make([]byte, length+2)
		if _, err := io.ReadFull(reader, value); err != nil {
			return nil, err
		}
		result[index] = string(value[:length])
	}
	return result, nil
}

func writeBulk(writer io.Writer, value string) {
	fmt.Fprintf(writer, "$%d\r\n%s\r\n", len(value), value)
}

func listRange(list []string, start, stop int) []string {
	if start < 0 {
		start += len(list)
	}
	if stop < 0 {
		stop += len(list)
	}
	start = max(0, start)
	stop = min(len(list)-1, stop)
	if len(list) == 0 || start > stop {
		return nil
	}
	return list[start : stop+1]
}

func (s *memoryValkey) serve(conn net.Conn) {
	defer conn.Close()
	args, err := readCommand(bufio.NewReader(conn))
	if err != nil || len(args) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch args[0] {
	case "HSET":
		if s.hashes[args[1]] == nil {
			s.hashes[args[1]] = make(map[string]string)
		}
		added := 0
		for index := 2; index < len(args); index += 2 {
			if _, ok := s.hashes[args[1]][args[index]]; !ok {
				added++
			}
			s.hashes[args[1]][args[index]] = args[index+1]
		}
		fmt.Fprintf(conn, ":%d\r\n", added)
	case "HGET":
		value, ok := s.hashes[args[1]][args[2]]
		if !ok {
			fmt.Fprint(conn, "$-1\r\n")
		} else {
			writeBulk(conn, value)
		}
	case "HGETALL":
		values := s.hashes[args[1]]
		fmt.Fprintf(conn, "*%d\r\n", len(values)*2)
		for field, value := range values {
			writeBulk(conn, field)
			writeBulk(conn, value)
		}
	case "HDEL":
		removed := 0
		for _, field := range args[2:] {
			if _, ok := s.hashes[args[1]][field]; ok {
				delete(s.hashes[args[1]], field)
				removed++
			}
		}
		fmt.Fprintf(conn, ":%d\r\n", removed)
	case "LPUSH":
		for _, value := range args[2:] {
			s.lists[args[1]] = append([]string{value}, s.lists[args[1]]...)
		}
		fmt.Fprintf(conn, ":%d\r\n", len(s.lists[args[1]]))
	case "LRANGE":
		start, _ := strconv.Atoi(args[2])
		stop, _ := strconv.Atoi(args[3])
		values := listRange(s.lists[args[1]], start, stop)
		fmt.Fprintf(conn, "*%d\r\n", len(values))
		for _, value := range values {
			writeBulk(conn, value)
		}
	case "LTRIM":
		start, _ := strconv.Atoi(args[2])
		stop, _ := strconv.Atoi(args[3])
		s.lists[args[1]] = append([]string(nil), listRange(s.lists[args[1]], start, stop)...)
		fmt.Fprint(conn, "+OK\r\n")
	case "DEL":
		removed := 0
		for _, key := range args[1:] {
			if _, ok := s.lists[key]; ok {
				delete(s.lists, key)
				removed++
			}
			if _, ok := s.hashes[key]; ok {
				delete(s.hashes, key)
				removed++
			}
		}
		fmt.Fprintf(conn, ":%d\r\n", removed)
	default:
		fmt.Fprint(conn, "-ERR unsupported\r\n")
	}
}

type stubRunner struct {
	reply  string
	err    error
	prompt string
}

func (s *stubRunner) RunTask(_ context.Context, prompt string) (string, error) {
	s.prompt = prompt
	return s.reply, s.err
}

type blockingRunner struct {
	started chan struct{}
	release chan struct{}
}

func (r *blockingRunner) RunTask(_ context.Context, _ string) (string, error) {
	close(r.started)
	<-r.release
	return "completed", nil
}

type captureJobManager struct {
	executor func(context.Context, jobs.Envelope) (string, error)
	source   func(time.Time) []jobs.Envelope
	enqueued []jobs.Envelope
}

func (m *captureJobManager) Register(jobType string, executor func(context.Context, jobs.Envelope) (string, error)) {
	if jobType == "project-run" {
		m.executor = executor
	}
}

func (m *captureJobManager) RegisterSource(source func(time.Time) []jobs.Envelope) {
	m.source = source
}

func (m *captureJobManager) Enqueue(env jobs.Envelope) (int, error) {
	m.enqueued = append(m.enqueued, env)
	return len(m.enqueued) - 1, nil
}

func newService(t *testing.T, runner TaskRunner) (*Service, *memoryValkey) {
	t.Helper()
	server := newMemoryValkey(t)
	client := valkey.New(server.addr)
	manager := jobs.New(client, nil)
	service := New(client, manager, runner, t.TempDir(), nil)
	service.now = func() time.Time {
		return time.Date(2026, 7, 23, 9, 0, 0, 0, time.Local)
	}
	return service, server
}

func TestCRUDValidationAndRunsDeletion(t *testing.T) {
	service, server := newService(t, &stubRunner{})
	if _, err := service.create("  "); err == nil {
		t.Fatal("empty name accepted")
	}
	project, err := service.create(" Tide report ")
	if err != nil || project.Name != "Tide report" || project.Enabled || project.Selector != "weekday morning" {
		t.Fatalf("create = %+v, %v", project, err)
	}
	name := "Renamed"
	task := strings.Repeat("x", 4096)
	selector := "tue,thu morning"
	enabled := true
	updated, err := service.update(updateRequest{
		ID: project.ID, Name: &name, Task: &task, Selector: &selector, Enabled: &enabled,
	})
	if err != nil || updated.Name != name || updated.Task != task || updated.Selector != selector || !updated.Enabled {
		t.Fatalf("update = %+v, %v", updated, err)
	}
	badSelector := "funday"
	if _, err := service.update(updateRequest{ID: project.ID, Selector: &badSelector}); err == nil {
		t.Fatal("invalid selector accepted")
	}
	tooLong := strings.Repeat("x", 4097)
	if _, err := service.update(updateRequest{ID: project.ID, Task: &tooLong}); err == nil {
		t.Fatal("oversized task accepted")
	}
	server.mu.Lock()
	server.lists[runsKey(project.ID)] = []string{`{"ok":true}`}
	server.mu.Unlock()
	if err := service.delete(project.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.get(project.ID); err == nil {
		t.Fatal("deleted project still exists")
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if _, ok := server.lists[runsKey(project.ID)]; ok {
		t.Fatal("run list was not deleted")
	}
}

func TestProjectCRUDPersistsAcrossServiceReconstruction(t *testing.T) {
	service, _ := newService(t, &stubRunner{})
	project, err := service.create("Persistent project")
	if err != nil {
		t.Fatal(err)
	}
	task := "Check persistence"
	selector := "mon,wed,fri afternoon"
	enabled := true
	if _, err := service.update(updateRequest{
		ID: project.ID, Task: &task, Selector: &selector, Enabled: &enabled,
	}); err != nil {
		t.Fatal(err)
	}

	reloaded := New(service.client, service.jobs, &stubRunner{}, service.dataDir, nil)
	stored, err := reloaded.get(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Name != "Persistent project" || stored.Task != task ||
		stored.Selector != selector || !stored.Enabled {
		t.Fatalf("reloaded project = %+v", stored)
	}
	renamed := "Persistent project renamed"
	if _, err := reloaded.update(updateRequest{ID: project.ID, Name: &renamed}); err != nil {
		t.Fatal(err)
	}
	stored, err = service.get(project.ID)
	if err != nil || stored.Name != renamed {
		t.Fatalf("updated project = %+v, %v", stored, err)
	}
	if err := reloaded.delete(project.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.get(project.ID); !errors.Is(err, errProjectNotFound) {
		t.Fatalf("deleted project returned: %v", err)
	}
}

func TestEmptyProjectTaskCannotBeEnabledOrExecuted(t *testing.T) {
	service, _ := newService(t, &stubRunner{})
	project, err := service.create("Draft")
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	if _, err := service.update(updateRequest{ID: project.ID, Enabled: &enabled}); err == nil {
		t.Fatal("enabled project without a task")
	}
	payload, _ := json.Marshal(map[string]any{"id": project.ID, "manual": true})
	if _, err := service.Execute(context.Background(), jobs.Envelope{ID: "job-empty", Payload: payload}); err == nil {
		t.Fatal("executed project without a task")
	}
}

func TestSourceDeduplicatesAcrossBucketEdges(t *testing.T) {
	runner := &stubRunner{reply: "Scheduled task completed"}
	service, _ := newService(t, runner)
	project, err := service.create("Scheduled")
	if err != nil {
		t.Fatal(err)
	}
	selector := "weekday morning"
	task := "Send the scheduled report"
	enabled := true
	if _, err := service.update(updateRequest{
		ID: project.ID, Task: &task, Selector: &selector, Enabled: &enabled,
	}); err != nil {
		t.Fatal(err)
	}
	morning := time.Date(2026, 7, 23, 9, 0, 0, 0, time.Local)
	first := service.Source(morning)
	if len(first) != 1 || first[0].VisibilityTimeoutSec != 1800 || first[0].MaxRetries != 1 {
		t.Fatalf("first source = %+v", first)
	}
	if _, err := service.Execute(context.Background(), first[0]); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(runner.prompt, "\n"+task) {
		t.Fatalf("scheduled task was not forwarded to runner: %q", runner.prompt)
	}
	runs := service.readRuns(project.ID, 5)
	if len(runs) != 1 || runs[0].Manual {
		t.Fatalf("scheduled runs = %+v", runs)
	}
	if second := service.Source(morning.Add(time.Hour)); len(second) != 0 {
		t.Fatalf("duplicate source = %+v", second)
	}
	nextDay := service.Source(morning.AddDate(0, 0, 1))
	if len(nextDay) != 1 {
		t.Fatalf("next bucket source = %+v", nextDay)
	}
	stored, err := service.get(project.ID)
	if err != nil || stored.LastEnqueuedBucket != "2026-07-24/morning" {
		t.Fatalf("stored = %+v, %v", stored, err)
	}
}

func TestNewRegistersScheduledProjectThroughChatRunner(t *testing.T) {
	server := newMemoryValkey(t)
	client := valkey.New(server.addr)
	manager := &captureJobManager{}
	runner := &stubRunner{reply: "Periodic action completed"}
	service := New(client, manager, runner, t.TempDir(), nil)
	service.now = func() time.Time {
		return time.Date(2026, 7, 23, 9, 0, 0, 0, time.Local)
	}
	if manager.source == nil || manager.executor == nil {
		t.Fatal("project scheduler source or executor was not registered")
	}
	project, err := service.create("Periodic action")
	if err != nil {
		t.Fatal(err)
	}
	task := "Send the main project text through chat as an action"
	selector := "weekday morning"
	enabled := true
	if _, err := service.update(updateRequest{
		ID: project.ID, Task: &task, Selector: &selector, Enabled: &enabled,
	}); err != nil {
		t.Fatal(err)
	}
	due := manager.source(service.now())
	if len(due) != 1 || due[0].Type != "project-run" || due[0].ProjectID != project.ID ||
		due[0].Priority != "scheduled" || due[0].Selector != selector {
		t.Fatalf("scheduled project = %+v", due)
	}
	if _, err := manager.executor(context.Background(), due[0]); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(runner.prompt, "\n"+task) {
		t.Fatalf("project task was not forwarded to chat runner: %q", runner.prompt)
	}
}

func TestExecuteCreatesScratchAndRecordsSummary(t *testing.T) {
	runner := &stubRunner{reply: strings.Repeat("é", 301)}
	service, _ := newService(t, runner)
	project, err := service.create("Research")
	if err != nil {
		t.Fatal(err)
	}
	task := "Inspect the source"
	if _, err := service.update(updateRequest{ID: project.ID, Task: &task}); err != nil {
		t.Fatal(err)
	}
	start := service.now()
	calls := 0
	service.now = func() time.Time {
		calls++
		if calls == 1 {
			return start
		}
		return start.Add(1250 * time.Millisecond)
	}
	payload, _ := json.Marshal(map[string]any{"id": project.ID, "manual": true})
	reply, err := service.Execute(context.Background(), jobs.Envelope{ID: "job-1", Payload: payload})
	if err != nil || len([]rune(reply)) != 300 {
		t.Fatalf("execute reply runes=%d err=%v", len([]rune(reply)), err)
	}
	scratch := service.dataDir + "/projects/" + project.ID
	if !strings.Contains(runner.prompt, `Project "Research" scratch directory: `+scratch) ||
		!strings.HasSuffix(runner.prompt, "\n"+task) {
		t.Fatalf("prompt = %q", runner.prompt)
	}
	if info, err := netFileStat(scratch); err != nil || !info {
		t.Fatalf("scratch missing: %v", err)
	}
	runs := service.readRuns(project.ID, 5)
	if len(runs) != 1 || !runs[0].OK || !runs[0].Manual || runs[0].JobID != "job-1" ||
		len([]rune(runs[0].Summary)) != 300 || runs[0].DurationMs != 1250 {
		t.Fatalf("runs = %+v", runs)
	}
	stored, _ := service.get(project.ID)
	if stored.LastRunTs != start.Add(1250*time.Millisecond).UnixMilli() {
		t.Fatalf("last run = %d", stored.LastRunTs)
	}
}

func TestExecutePreservesConcurrentProjectChanges(t *testing.T) {
	runner := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	service, _ := newService(t, runner)
	project, err := service.create("Original")
	if err != nil {
		t.Fatal(err)
	}
	task := "Original task"
	if _, err := service.update(updateRequest{ID: project.ID, Task: &task}); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"id": project.ID, "manual": true})
	done := make(chan error, 1)
	go func() {
		_, err := service.Execute(context.Background(), jobs.Envelope{ID: "job-race", Payload: payload})
		done <- err
	}()
	<-runner.started

	name := "Updated while running"
	task = "Updated task"
	selector := "weekday morning"
	enabled := true
	if _, err := service.update(updateRequest{
		ID: project.ID, Name: &name, Task: &task, Selector: &selector, Enabled: &enabled,
	}); err != nil {
		t.Fatal(err)
	}
	if due := service.Source(service.now()); len(due) != 1 {
		t.Fatalf("scheduled source = %+v", due)
	}
	close(runner.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	stored, err := service.get(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Name != name || stored.Task != task || !stored.Enabled ||
		stored.LastEnqueuedBucket != "2026-07-23/morning" || stored.LastRunTs == 0 {
		t.Fatalf("concurrent changes lost: %+v", stored)
	}
}

func TestDeleteDuringExecutionDoesNotResurrectProject(t *testing.T) {
	runner := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	service, server := newService(t, runner)
	project, err := service.create("Delete while running")
	if err != nil {
		t.Fatal(err)
	}
	task := "Complete this task"
	if _, err := service.update(updateRequest{ID: project.ID, Task: &task}); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"id": project.ID, "manual": true})
	done := make(chan error, 1)
	go func() {
		_, err := service.Execute(context.Background(), jobs.Envelope{ID: "job-delete", Payload: payload})
		done <- err
	}()
	<-runner.started
	if err := service.delete(project.ID); err != nil {
		t.Fatal(err)
	}
	close(runner.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := service.get(project.ID); !errors.Is(err, errProjectNotFound) {
		t.Fatalf("deleted project returned: %v", err)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if _, ok := server.lists[runsKey(project.ID)]; ok {
		t.Fatal("execution recreated deleted project runs")
	}
}

func netFileStat(path string) (bool, error) {
	info, err := os.Stat(path)
	return err == nil && info.IsDir(), err
}
