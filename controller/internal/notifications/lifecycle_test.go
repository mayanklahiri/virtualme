package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLifecycleFirstBootAndCleanShutdown(t *testing.T) {
	root := t.TempDir()
	lifecycle := newTestLifecycle(root)
	if err := lifecycle.Startup(context.Background()); err != nil {
		t.Fatal(err)
	}
	marker := readTestMarker(t, root)
	if marker.State != "running" || marker.RunID == "" {
		t.Fatalf("startup marker = %#v", marker)
	}
	if err := lifecycle.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	marker = readTestMarker(t, root)
	if marker.State != "clean" || marker.Reason != "container-stop" || marker.NotificationID == "" {
		t.Fatalf("shutdown marker = %#v", marker)
	}
	info, err := os.Stat(filepath.Join(root, lifecycleFileName))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("marker mode: %v %v", info, err)
	}
}

func TestLifecyclePlannedRestartMarker(t *testing.T) {
	root := t.TempDir()
	lifecycle := newTestLifecycle(root)
	if err := lifecycle.Startup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.PlanConfigRestart(context.Background()); err != nil {
		t.Fatal(err)
	}
	marker := readTestMarker(t, root)
	if marker.State != "planned-restart" || marker.Reason != "config-restart" || marker.DeadlineMS == 0 {
		t.Fatalf("planned marker = %#v", marker)
	}
}

func TestMalformedRecoveryPendingIsValidAndByteIdentical(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, lifecycleFileName)
	if err := os.WriteFile(path, []byte(`{"version":1,"state":"running"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	lifecycle := newTestLifecycle(root)
	var attempts []Notification
	lifecycle.service.createExactHook = func(_ context.Context, notification Notification) (Notification, error) {
		attempts = append(attempts, notification)
		if len(attempts) == 1 {
			return Notification{}, errors.New("injected persistence failure")
		}
		return notification, nil
	}
	if err := lifecycle.Startup(context.Background()); err == nil {
		t.Fatal("startup unexpectedly succeeded")
	}
	pending := readTestMarker(t, root)
	if err := validateLifecycleMarker(pending); err != nil {
		t.Fatalf("pending marker is not recoverable: %v: %#v", err, pending)
	}
	if pending.State != "clean-pending" || pending.Reason != "unclean-recovery" ||
		!validULID(pending.RunID) || pending.StartedAtMS <= 0 {
		t.Fatalf("pending marker = %#v", pending)
	}
	retry := NewLifecycle(lifecycle.service)
	if err := retry.Startup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 {
		t.Fatalf("create attempts=%d", len(attempts))
	}
	first, _ := json.Marshal(attempts[0])
	second, _ := json.Marshal(attempts[1])
	if !bytes.Equal(first, second) {
		t.Fatalf("retry changed immutable notification:\n%s\n%s", first, second)
	}
	if marker := readTestMarker(t, root); marker.State != "running" {
		t.Fatalf("final marker=%#v", marker)
	}
}

func TestUnreadableMarkerUsesRecoverablePending(t *testing.T) {
	root := t.TempDir()
	lifecycle := newTestLifecycle(root)
	lifecycle.fs.readFile = func(string) ([]byte, error) { return nil, errors.New("injected read failure") }
	var captured []Notification
	lifecycle.service.createExactHook = func(_ context.Context, notification Notification) (Notification, error) {
		captured = append(captured, notification)
		if len(captured) == 1 {
			return Notification{}, errors.New("injected persistence failure")
		}
		return notification, nil
	}
	if err := lifecycle.Startup(context.Background()); err == nil {
		t.Fatal("startup unexpectedly succeeded")
	}
	pending := readTestMarker(t, root)
	if err := validateLifecycleMarker(pending); err != nil {
		t.Fatalf("unreadable recovery marker=%#v: %v", pending, err)
	}
	retry := NewLifecycle(lifecycle.service)
	if err := retry.Startup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(captured) != 2 || captured[0].Subtype != "unclean-startup" ||
		!validULID(captured[0].ID) {
		t.Fatalf("notifications=%#v", captured)
	}
	first, _ := json.Marshal(captured[0])
	second, _ := json.Marshal(captured[1])
	if !bytes.Equal(first, second) {
		t.Fatalf("unreadable retry changed bytes:\n%s\n%s", first, second)
	}
}

func TestLifecycleMarkerStateValidation(t *testing.T) {
	notification, err := validateCreate(CreateRequest{
		ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Type: "info", Subtype: "clean-shutdown",
		Sender: "controller", Title: "Controller shutting down",
		Summary:  "The controller received a shutdown request and saved its state.",
		Renderer: "lifecycle", Detail: json.RawMessage(`{}`),
	}, testClock())
	if err != nil {
		t.Fatal(err)
	}
	base := lifecycleMarker{
		Version: 1, State: "running", RunID: "01ARZ3NDEKTSV4RRFFQ69G5FAW",
		StartedAtMS: testClock().UnixMilli(), UpdatedAtMS: testClock().UnixMilli(),
	}
	if err := validateLifecycleMarker(base); err != nil {
		t.Fatal(err)
	}
	cases := map[string]lifecycleMarker{
		"running reason": func() lifecycleMarker { m := base; m.Reason = "container-stop"; return m }(),
		"planned deadline": func() lifecycleMarker {
			m := base
			m.State, m.Reason, m.DeadlineMS = "planned-restart", "config-restart", m.UpdatedAtMS+119999
			return m
		}(),
		"clean pending data": func() lifecycleMarker {
			m := base
			m.State, m.Reason, m.NotificationID = "clean", "container-stop", notification.ID
			m.PendingNotification = &notification
			return m
		}(),
		"pending missing notification": func() lifecycleMarker {
			m := base
			m.State, m.Reason, m.NotificationID = "clean-pending", "container-stop", notification.ID
			return m
		}(),
		"pending mismatch": func() lifecycleMarker {
			m := base
			m.State, m.Reason, m.NotificationID = "clean-pending", "config-restart", notification.ID
			m.PendingNotification = &notification
			return m
		}(),
		"unexpected deadline": func() lifecycleMarker {
			m := base
			m.State, m.Reason, m.NotificationID, m.DeadlineMS = "clean", "container-stop", notification.ID, 1
			return m
		}(),
	}
	for name, marker := range cases {
		t.Run(name, func(t *testing.T) {
			if err := validateLifecycleMarker(marker); err == nil {
				t.Fatalf("accepted %#v", marker)
			}
		})
	}
}

type fakeSyncFile struct {
	writeErr, syncErr, closeErr error
	data                        []byte
}

func (f *fakeSyncFile) Write(data []byte) (int, error) {
	f.data = append(f.data, data...)
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	return len(data), nil
}
func (f *fakeSyncFile) Sync() error  { return f.syncErr }
func (f *fakeSyncFile) Close() error { return f.closeErr }

type fakeSyncDir struct{ syncErr, closeErr error }

func (d *fakeSyncDir) Sync() error  { return d.syncErr }
func (d *fakeSyncDir) Close() error { return d.closeErr }
func (d *fakeSyncDir) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}

func TestAtomicMarkerWriteFailureStagesAndCleanup(t *testing.T) {
	baseError := errors.New("injected")
	marker := lifecycleMarker{
		Version: 1, State: "running", RunID: "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		StartedAtMS: testClock().UnixMilli(), UpdatedAtMS: testClock().UnixMilli(),
	}
	for _, stage := range []string{"mkdir", "open-temp", "write", "file-sync", "close", "rename", "open-dir", "dir-sync"} {
		t.Run(stage, func(t *testing.T) {
			lifecycle := newTestLifecycle(t.TempDir())
			file := new(fakeSyncFile)
			dir := new(fakeSyncDir)
			removed := false
			lifecycle.fs = lifecycleFS{
				mkdirAll: func(string, os.FileMode) error {
					if stage == "mkdir" {
						return baseError
					}
					return nil
				},
				readFile: os.ReadFile,
				openTemp: func(string, string) (syncFile, string, error) {
					if stage == "open-temp" {
						return nil, "", baseError
					}
					if stage == "write" {
						file.writeErr = baseError
					}
					if stage == "file-sync" {
						file.syncErr = baseError
					}
					if stage == "close" {
						file.closeErr = baseError
					}
					return file, "/tmp/random-marker", nil
				},
				rename: func(string, string) error {
					if stage == "rename" {
						return baseError
					}
					return nil
				},
				remove: func(string) error { removed = true; return nil },
				openDir: func(string) (syncDir, error) {
					if stage == "open-dir" {
						return nil, baseError
					}
					if stage == "dir-sync" {
						dir.syncErr = baseError
					}
					return dir, nil
				},
			}
			if err := lifecycle.writeMarker(marker); err == nil ||
				!strings.Contains(err.Error(), "injected") {
				t.Fatalf("stage %s error=%v", stage, err)
			}
			renamed := stage == "open-dir" || stage == "dir-sync"
			if !renamed && stage != "mkdir" && stage != "open-temp" && !removed {
				t.Fatalf("stage %s did not clean temporary file", stage)
			}
		})
	}
}

func TestAtomicMarkerWriteUsesRandomMode0600File(t *testing.T) {
	root := t.TempDir()
	lifecycle := newTestLifecycle(root)
	marker := lifecycleMarker{
		Version: 1, State: "running", RunID: "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		StartedAtMS: testClock().UnixMilli(), UpdatedAtMS: testClock().UnixMilli(),
	}
	if err := lifecycle.writeMarker(marker); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, lifecycleFileName))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("marker mode=%v err=%v", info.Mode().Perm(), err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != lifecycleFileName {
		t.Fatalf("temporary files remain: %#v", entries)
	}
}

func TestLifecycleStartupClassifiesCleanPlannedAndStaleRuns(t *testing.T) {
	tests := []struct {
		name        string
		marker      lifecycleMarker
		wantSubtype string
	}{
		{
			name: "clean container stop",
			marker: lifecycleMarker{
				Version: 1, State: "clean", RunID: "01ARZ3NDEKTSV4RRFFQ69G5FAV",
				StartedAtMS: testClock().UnixMilli() - 10, UpdatedAtMS: testClock().UnixMilli() - 1,
				Reason: "container-stop", NotificationID: "01ARZ3NDEKTSV4RRFFQ69G5FAW",
			},
		},
		{
			name: "clean config restart",
			marker: lifecycleMarker{
				Version: 1, State: "clean", RunID: "01ARZ3NDEKTSV4RRFFQ69G5FAV",
				StartedAtMS: testClock().UnixMilli() - 10, UpdatedAtMS: testClock().UnixMilli() - 1,
				Reason: "config-restart", NotificationID: "01ARZ3NDEKTSV4RRFFQ69G5FAW",
			},
			wantSubtype: "config-restart-startup",
		},
		{
			name: "active planned restart",
			marker: lifecycleMarker{
				Version: 1, State: "planned-restart", RunID: "01ARZ3NDEKTSV4RRFFQ69G5FAV",
				StartedAtMS: testClock().UnixMilli() - 10, UpdatedAtMS: testClock().UnixMilli() - 1,
				Reason: "config-restart", DeadlineMS: testClock().UnixMilli() + 119999,
			},
			wantSubtype: "config-restart-startup",
		},
		{
			name: "stale planned restart",
			marker: lifecycleMarker{
				Version: 1, State: "planned-restart", RunID: "01ARZ3NDEKTSV4RRFFQ69G5FAV",
				StartedAtMS: testClock().UnixMilli() - 420002, UpdatedAtMS: testClock().UnixMilli() - 420001,
				Reason: "config-restart", DeadlineMS: testClock().UnixMilli() - 300001,
			},
			wantSubtype: "unclean-startup",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			encoded, _ := json.Marshal(test.marker)
			if err := os.WriteFile(filepath.Join(root, lifecycleFileName), encoded, 0o600); err != nil {
				t.Fatal(err)
			}
			lifecycle := newTestLifecycle(root)
			var created []Notification
			lifecycle.service.createExactHook = func(_ context.Context, notification Notification) (Notification, error) {
				created = append(created, notification)
				return notification, nil
			}
			if err := lifecycle.Startup(context.Background()); err != nil {
				t.Fatal(err)
			}
			if test.wantSubtype == "" {
				if len(created) != 0 {
					t.Fatalf("quiet startup created %#v", created)
				}
			} else if len(created) != 1 || created[0].Subtype != test.wantSubtype {
				t.Fatalf("created=%#v want %s", created, test.wantSubtype)
			}
		})
	}
}

func TestLifecyclePlannedShutdownAndPendingRecovery(t *testing.T) {
	root := t.TempDir()
	lifecycle := newTestLifecycle(root)
	var created []Notification
	lifecycle.service.createExactHook = func(_ context.Context, notification Notification) (Notification, error) {
		created = append(created, notification)
		return notification, nil
	}
	if err := lifecycle.Startup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.PlanConfigRestart(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	marker := readTestMarker(t, root)
	if marker.State != "clean" || marker.Reason != "config-restart" ||
		len(created) != 1 || created[0].Subtype != "config-restart-shutdown" {
		t.Fatalf("marker=%#v created=%#v", marker, created)
	}
	pending := marker
	pending.State = "clean-pending"
	pending.PendingNotification = &created[0]
	if err := lifecycle.writeMarker(pending); err != nil {
		t.Fatal(err)
	}
	created = nil
	retry := NewLifecycle(lifecycle.service)
	if err := retry.Startup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(created) != 2 || created[0].ID != pending.NotificationID ||
		created[1].Subtype != "config-restart-startup" {
		t.Fatalf("pending recovery created=%#v", created)
	}
}

func newTestLifecycle(root string) *Lifecycle {
	service := &Service{dataDir: root, clock: testClock, ids: newULIDGenerator(testClock, zeroReader{})}
	service.createExactHook = func(_ context.Context, notification Notification) (Notification, error) {
		return notification, nil
	}
	return NewLifecycle(service)
}

func readTestMarker(t *testing.T, root string) lifecycleMarker {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, lifecycleFileName))
	if err != nil {
		t.Fatal(err)
	}
	var marker lifecycleMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		t.Fatal(err)
	}
	return marker
}

func testClock() time.Time { return time.UnixMilli(1_700_000_000_000) }

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}
