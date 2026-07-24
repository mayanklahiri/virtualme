package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (a *Agent) beginTask() error {
	if err := os.MkdirAll(a.cfg.DataDir, 0o755); err != nil {
		return err
	}
	if err := a.pruneTasks(); err != nil {
		return err
	}
	a.taskID = fmt.Sprintf("%d", time.Now().UnixNano())
	a.taskDir = filepath.Join(a.cfg.DataDir, a.taskID)
	a.step = 0
	if local, ok := a.tools.(*localTools); ok {
		local.resetTask(a.taskID)
	}
	return os.MkdirAll(a.taskDir, 0o755)
}

func (a *Agent) pruneTasks() error {
	entries, err := os.ReadDir(a.cfg.DataDir)
	if err != nil {
		return err
	}
	directories := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			directories = append(directories, entry.Name())
		}
	}
	sort.Strings(directories)
	remove := len(directories) - a.cfg.KeepTasks + 1
	for index := 0; index < remove; index++ {
		if err := os.RemoveAll(filepath.Join(a.cfg.DataDir, directories[index])); err != nil {
			return err
		}
	}
	return nil
}

func (a *Agent) recordStep(ctx context.Context, call ToolCall, result ToolResult, toolErr error) string {
	var args any
	if json.Unmarshal([]byte(call.Function.Arguments), &args) != nil {
		args = call.Function.Arguments
	}
	event := map[string]any{
		"type": "agent-step", "taskId": a.taskID, "n": a.step,
		"tool": call.Function.Name, "args": args, "summary": result.Summary,
	}
	if result.Text != "" {
		event["text"] = truncatePromptText(result.Text, observationTextCap)
	}
	if toolErr != nil {
		event["error"] = toolErr.Error()
	}
	image := result.ImageJPEG
	if len(image) == 0 {
		if local, ok := a.tools.(*localTools); ok {
			captureCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			image, _ = local.screenshot(captureCtx)
			cancel()
		}
	}
	if len(image) > 0 {
		path := filepath.Join(a.taskDir, fmt.Sprintf("step-%d.jpg", a.step))
		if os.WriteFile(path, image, 0o600) == nil {
			if thumbnail := a.thumbnail(ctx, path); len(thumbnail) > 0 {
				event["screenshot"] = "data:image/jpeg;base64," + encodeBase64(thumbnail)
			}
		}
	}
	encoded, _ := json.Marshal(event)
	logFile, err := os.OpenFile(filepath.Join(a.taskDir, "steps.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err == nil {
		_, _ = logFile.Write(append(encoded, '\n'))
		_ = logFile.Close()
	}
	a.cfg.Broadcast(encoded)
	thumbnail, _ := event["screenshot"].(string)
	return thumbnail
}

func (a *Agent) thumbnail(ctx context.Context, source string) []byte {
	local, ok := a.tools.(*localTools)
	if !ok {
		return nil
	}
	destination := strings.TrimSuffix(source, ".jpg") + "-thumb.jpg"
	thumbnailCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, _, err := local.runner.Run(thumbnailCtx, local.cfg.ConvertPath, []string{
		source, "-resize", "480x270>", "-quality", "60", "-define", "jpeg:extent=32kb", destination,
	}, nil, "")
	if err != nil {
		return nil
	}
	data, _ := os.ReadFile(destination)
	_ = os.Remove(destination)
	if len(data) > 32*1024 {
		return nil
	}
	return data
}
