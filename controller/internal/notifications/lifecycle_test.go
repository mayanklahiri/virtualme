package notifications

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
