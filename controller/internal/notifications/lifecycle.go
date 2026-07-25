package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const lifecycleFileName = "controller-lifecycle.json"

type lifecycleMarker struct {
	Version             int           `json:"version"`
	State               string        `json:"state"`
	RunID               string        `json:"runId"`
	StartedAtMS         int64         `json:"startedAtMs"`
	UpdatedAtMS         int64         `json:"updatedAtMs"`
	Reason              string        `json:"reason"`
	DeadlineMS          int64         `json:"deadlineMs"`
	NotificationID      string        `json:"notificationId"`
	PendingNotification *Notification `json:"pendingNotification"`
}

// Lifecycle manages durable crash/clean evidence independently from Valkey.
type Lifecycle struct {
	mu      sync.Mutex
	service *Service
	path    string
	marker  lifecycleMarker
}

// NewLifecycle binds lifecycle evidence to a notification service.
func NewLifecycle(service *Service) *Lifecycle {
	return &Lifecycle{service: service, path: filepath.Join(service.dataDir, lifecycleFileName)}
}

// Startup recovers prior evidence and starts a new run.
func (l *Lifecycle) Startup(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.service.clock().UnixMilli()
	previous, status, err := l.readMarker()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		status = "unreadable"
	}
	firstBoot := errors.Is(err, os.ErrNotExist)

	switch {
	case firstBoot:
		// First-ever boot is intentionally quiet.
	case status == "valid" && previous.State == "clean-pending":
		if err := l.recoverPending(ctx, previous); err != nil {
			return err
		}
		if previous.Reason == "config-restart" {
			if err := l.restartComplete(ctx, previous, "clean"); err != nil {
				return err
			}
		}
	case status == "valid" && previous.State == "clean":
		if previous.Reason == "config-restart" {
			if err := l.restartComplete(ctx, previous, "clean"); err != nil {
				return err
			}
		}
	case status == "valid" && previous.State == "planned-restart" && previous.DeadlineMS >= now-300000:
		if err := l.restartComplete(ctx, previous, "planned-restart"); err != nil {
			return err
		}
	default:
		if err := l.uncleanRecovery(ctx, previous, status, now); err != nil {
			return err
		}
	}

	runID, err := l.service.ids.next()
	if err != nil {
		return err
	}
	l.marker = lifecycleMarker{
		Version: 1, State: "running", RunID: runID,
		StartedAtMS: now, UpdatedAtMS: now,
	}
	return l.writeMarker(l.marker)
}

func (l *Lifecycle) uncleanRecovery(ctx context.Context, previous lifecycleMarker, status string, now int64) error {
	occurred := now
	detail := map[string]any{"markerStatus": status}
	if status == "valid" {
		if previous.UpdatedAtMS > 0 && previous.UpdatedAtMS <= now+300000 {
			occurred = previous.UpdatedAtMS
			detail["lastMarkerAtMs"] = previous.UpdatedAtMS
		}
		if validULID(previous.RunID) {
			detail["previousRunId"] = previous.RunID
		}
		if previous.StartedAtMS > 0 && previous.StartedAtMS <= now+300000 {
			detail["previousStartedAtMs"] = previous.StartedAtMS
		}
	}
	notification, err := l.reserve(CreateRequest{
		Type: "error", Subtype: "unclean-startup", Sender: "controller",
		Title:        "Controller restarted unexpectedly",
		Summary:      "The previous controller run did not shut down cleanly.",
		OccurredAtMS: occurred, Renderer: "lifecycle", Detail: marshalDetail(detail),
	})
	if err != nil {
		return err
	}
	pending := lifecycleMarker{
		Version: 1, State: "clean-pending", RunID: previous.RunID,
		StartedAtMS: previous.StartedAtMS, UpdatedAtMS: now,
		Reason: "unclean-recovery", NotificationID: notification.ID,
		PendingNotification: &notification,
	}
	if err := l.writeMarker(pending); err != nil {
		return err
	}
	_, err = l.service.createExact(ctx, notification)
	return err
}

func (l *Lifecycle) recoverPending(ctx context.Context, marker lifecycleMarker) error {
	if marker.PendingNotification == nil || marker.NotificationID != marker.PendingNotification.ID {
		return l.uncleanRecovery(ctx, marker, "malformed", l.service.clock().UnixMilli())
	}
	_, err := l.service.createExact(ctx, *marker.PendingNotification)
	return err
}

func (l *Lifecycle) restartComplete(ctx context.Context, previous lifecycleMarker, recoveredFrom string) error {
	detail := map[string]any{
		"previousRunId": previous.RunID, "previousStartedAtMs": previous.StartedAtMS,
		"lastMarkerAtMs": previous.UpdatedAtMS,
	}
	if recoveredFrom == "planned-restart" {
		detail = map[string]any{"recoveredFrom": recoveredFrom}
	}
	notification, err := l.reserve(CreateRequest{
		Type: "success", Subtype: "config-restart-startup", Sender: "controller",
		Title:    "Configuration restart complete",
		Summary:  "The controller restarted cleanly after configuration changed.",
		Renderer: "lifecycle", Detail: marshalDetail(detail),
	})
	if err != nil {
		return err
	}
	pending := lifecycleMarker{
		Version: 1, State: "clean-pending", RunID: previous.RunID,
		StartedAtMS: previous.StartedAtMS, UpdatedAtMS: notification.CreatedAtMS,
		Reason: "config-restart-startup", NotificationID: notification.ID,
		PendingNotification: &notification,
	}
	if err := l.writeMarker(pending); err != nil {
		return err
	}
	_, err = l.service.createExact(ctx, notification)
	return err
}

// PlanConfigRestart records deliberate restart intent before the 202 response.
func (l *Lifecycle) PlanConfigRestart(_ context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	marker, status, err := l.readMarker()
	if err != nil || status != "valid" || marker.State != "running" {
		return errors.New("controller lifecycle marker is unavailable")
	}
	now := l.service.clock().UnixMilli()
	marker.State = "planned-restart"
	marker.Reason = "config-restart"
	marker.UpdatedAtMS = now
	marker.DeadlineMS = now + 120000
	marker.NotificationID = ""
	marker.PendingNotification = nil
	if err := l.writeMarker(marker); err != nil {
		return err
	}
	l.marker = marker
	return nil
}

// Shutdown durably records the clean stop notification and marker.
func (l *Lifecycle) Shutdown(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	marker, status, err := l.readMarker()
	if err != nil || status != "valid" {
		return errors.New("controller lifecycle marker is unavailable")
	}
	now := l.service.clock().UnixMilli()
	planned := marker.State == "planned-restart" && marker.DeadlineMS >= now-300000
	reason, subtype := "container-stop", "clean-shutdown"
	title := "Controller shutting down"
	summary := "The controller received a shutdown request and saved its state."
	if planned {
		reason, subtype = "config-restart", "config-restart-shutdown"
		title = "Controller restarting"
		summary = "The controller is restarting to apply configuration changes."
	}
	notification, err := l.reserve(CreateRequest{
		Type: "info", Subtype: subtype, Sender: "controller", Title: title, Summary: summary,
		OccurredAtMS: now, Renderer: "lifecycle", Detail: marshalDetail(map[string]any{
			"runId": marker.RunID, "startedAtMs": marker.StartedAtMS,
			"shutdownAtMs": now, "reason": reason,
		}),
	})
	if err != nil {
		return err
	}
	pending := lifecycleMarker{
		Version: 1, State: "clean-pending", RunID: marker.RunID,
		StartedAtMS: marker.StartedAtMS, UpdatedAtMS: now, Reason: reason,
		NotificationID: notification.ID, PendingNotification: &notification,
	}
	if err := l.writeMarker(pending); err != nil {
		return err
	}
	if _, err := l.service.createExact(ctx, notification); err != nil {
		return err
	}
	pending.State = "clean"
	pending.PendingNotification = nil
	if err := l.writeMarker(pending); err != nil {
		return err
	}
	l.marker = pending
	return nil
}

func (l *Lifecycle) reserve(request CreateRequest) (Notification, error) {
	notification, err := validateCreate(request, l.service.clock())
	if err != nil {
		return Notification{}, err
	}
	notification.ID, err = l.service.ids.next()
	return notification, err
}

func marshalDetail(value any) json.RawMessage {
	encoded, _ := json.Marshal(value)
	return encoded
}

func (l *Lifecycle) readMarker() (lifecycleMarker, string, error) {
	data, err := os.ReadFile(l.path)
	if err != nil {
		return lifecycleMarker{}, "absent", err
	}
	var marker lifecycleMarker
	if json.Unmarshal(data, &marker) != nil || marker.Version != 1 ||
		!validULID(marker.RunID) || marker.StartedAtMS <= 0 || marker.UpdatedAtMS <= 0 {
		return lifecycleMarker{}, "malformed", nil
	}
	switch marker.State {
	case "running", "planned-restart", "clean-pending", "clean":
	default:
		return lifecycleMarker{}, "malformed", nil
	}
	return marker, "valid", nil
}

func (l *Lifecycle) writeMarker(marker lifecycleMarker) error {
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return err
	}
	encoded, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	temp, err := os.OpenFile(l.path+".tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	cleanup := func(writeErr error) error {
		_ = temp.Close()
		_ = os.Remove(l.path + ".tmp")
		return writeErr
	}
	if _, err := temp.Write(append(encoded, '\n')); err != nil {
		return cleanup(err)
	}
	if err := temp.Sync(); err != nil {
		return cleanup(err)
	}
	if err := temp.Close(); err != nil {
		return cleanup(err)
	}
	if err := os.Rename(l.path+".tmp", l.path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(l.path))
	if err != nil {
		return err
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync lifecycle directory: %w", err)
	}
	return nil
}
