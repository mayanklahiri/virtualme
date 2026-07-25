package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strings"

	"github.com/mayanklahiri/virtualme/controller/internal/jobs"
)

const (
	defaultBytesPerToken = 3.0
	contextTokenMargin   = 512
	minCompletionTokens  = 1024
	imageTokenEstimate   = 256
	olderToolTextCap     = 512
	minObservationCap    = 4 * 1024
	maxObservationCap    = 64 * 1024

	observationToolStub       = "Observation captured; use the following observation message."
	supersededToolStub        = "[observation superseded]"
	supersededObservation     = "[observation superseded by a newer one]"
	unchangedObservation      = "Page unchanged since the last observation."
	promptTruncationSuffix    = "\n…[truncated to fit model context]"
	observationTruncateSuffix = "\n…[observation reduced to fit model context]"
)

type promptEstimate struct {
	textBytes   int
	fixedTokens int
}

func (estimate promptEstimate) tokens(bytesPerToken float64) int {
	if bytesPerToken <= 0 {
		bytesPerToken = defaultBytesPerToken
	}
	return int(math.Ceil(float64(estimate.textBytes)/bytesPerToken)) + estimate.fixedTokens
}

type preparedPrompt struct {
	Messages        []PromptMessage
	HistoryEnd      int
	Estimate        promptEstimate
	EstimatedTokens int
	MaxTokens       int
	Degradations    []string
}

func observationPromptCap(contextTokens int) int {
	if contextTokens <= 0 {
		contextTokens = defaultContextTokens
	}
	limit := contextTokens * 3 / 4
	return min(max(limit, minObservationCap), maxObservationCap)
}

func estimatePrompt(messages []PromptMessage, definitions []map[string]any) promptEstimate {
	estimate := promptEstimate{}
	encodedTools, _ := json.Marshal(definitions)
	estimate.textBytes += len(encodedTools)
	for _, message := range messages {
		estimate.textBytes += len(message.Role) + len(message.ToolCallID) + 12
		if len(message.ToolCalls) > 0 {
			encoded, _ := json.Marshal(message.ToolCalls)
			estimate.textBytes += len(encoded)
		}
		switch content := message.Content.(type) {
		case string:
			estimate.textBytes += len(content)
		case []map[string]any:
			for _, part := range content {
				if part["type"] == "image_url" {
					estimate.fixedTokens += imageTokenEstimate
					continue
				}
				if text, ok := part["text"].(string); ok {
					estimate.textBytes += len(text)
					continue
				}
				encoded, _ := json.Marshal(part)
				estimate.textBytes += len(encoded)
			}
		default:
			encoded, _ := json.Marshal(content)
			estimate.textBytes += len(encoded)
		}
	}
	return estimate
}

func adaptiveMaxTokens(contextTokens, estimatedPrompt int) int {
	available := contextTokens - estimatedPrompt - contextTokenMargin
	return min(max(available, minCompletionTokens), max(1, contextTokens/4))
}

func cloneMessages(messages []PromptMessage) []PromptMessage {
	return append([]PromptMessage(nil), messages...)
}

func toolRoundStarts(messages []PromptMessage, historyEnd int) []int {
	starts := make([]int, 0)
	for index := max(0, historyEnd); index < len(messages); index++ {
		if messages[index].Role == "assistant" && len(messages[index].ToolCalls) > 0 {
			starts = append(starts, index)
		}
	}
	return starts
}

func firstLineCapped(text string, limit int) string {
	if line, _, found := strings.Cut(text, "\n"); found {
		text = line
	}
	return truncatePromptText(text, limit)
}

func compactOlderToolText(messages []PromptMessage, historyEnd int) ([]PromptMessage, bool) {
	starts := toolRoundStarts(messages, historyEnd)
	if len(starts) < 2 {
		return messages, false
	}
	compacted := cloneMessages(messages)
	changed := false
	newest := starts[len(starts)-1]
	for index := starts[0]; index < newest; index++ {
		if compacted[index].Role != "tool" {
			continue
		}
		text, ok := compacted[index].Content.(string)
		if !ok || text == observationToolStub || text == supersededToolStub {
			continue
		}
		reduced := firstLineCapped(text, olderToolTextCap)
		if reduced != text {
			compacted[index].Content = reduced
			changed = true
		}
	}
	return compacted, changed
}

func dropOldestToolRound(messages []PromptMessage, historyEnd int) ([]PromptMessage, bool) {
	starts := toolRoundStarts(messages, historyEnd)
	if len(starts) < 2 {
		return messages, false
	}
	return append(cloneMessages(messages[:starts[0]]), messages[starts[1]:]...), true
}

func observationText(message PromptMessage) (string, bool) {
	if message.Role != "user" {
		return "", false
	}
	switch content := message.Content.(type) {
	case string:
		if strings.HasPrefix(content, "Observation from ") {
			return content, true
		}
	case []map[string]any:
		for _, part := range content {
			if part["type"] == "image_url" {
				for _, candidate := range content {
					if text, ok := candidate["text"].(string); ok {
						return text, true
					}
				}
				return "", true
			}
		}
	}
	return "", false
}

func isUnchangedObservation(message PromptMessage) bool {
	text, ok := observationText(message)
	return ok && strings.Contains(text, unchangedObservation)
}

func shrinkLatestObservation(messages []PromptMessage, dropImage bool) ([]PromptMessage, bool) {
	for index := len(messages) - 1; index >= 0; index-- {
		text, ok := observationText(messages[index])
		if !ok || isUnchangedObservation(messages[index]) {
			continue
		}
		reducedLimit := max(256, len(text)/2)
		reduced := text
		if len(reduced) > reducedLimit {
			if reducedLimit <= len(observationTruncateSuffix) {
				reduced = reduced[:reducedLimit]
			} else {
				reduced = reduced[:reducedLimit-len(observationTruncateSuffix)] + observationTruncateSuffix
			}
		}
		compacted := cloneMessages(messages)
		if parts, multimodal := messages[index].Content.([]map[string]any); multimodal {
			next := make([]map[string]any, 0, len(parts))
			for _, part := range parts {
				if part["type"] == "image_url" && dropImage {
					continue
				}
				copyPart := make(map[string]any, len(part))
				for key, value := range part {
					copyPart[key] = value
				}
				if copyPart["type"] == "text" {
					copyPart["text"] = reduced
				}
				next = append(next, copyPart)
			}
			compacted[index].Content = next
		} else {
			compacted[index].Content = reduced
		}
		return compacted, reduced != text || dropImage
	}
	return messages, false
}

func (a *Agent) toolDefinitions() []map[string]any {
	definitions := make([]map[string]any, 0)
	for _, tool := range a.tools.Definitions() {
		definitions = append(definitions, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": tool.Name, "description": tool.Description, "parameters": tool.Schema,
			},
		})
	}
	return definitions
}

func (a *Agent) preparePrompt(messages []PromptMessage, historyEnd int) (preparedPrompt, error) {
	prepared := compactTaskMessages(messages, historyEnd)
	historyEnd = min(historyEnd, len(prepared))
	degradations := make([]string, 0)
	var changed bool
	prepared, changed = compactOlderToolText(prepared, historyEnd)
	if changed {
		degradations = append(degradations, "older tool results compacted")
	}
	definitions := a.toolDefinitions()
	estimate := estimatePrompt(prepared, definitions)
	estimatedTokens := estimate.tokens(a.bytesPerToken)
	maxPromptTokens := a.cfg.ContextTokens - contextTokenMargin - minCompletionTokens

	for estimatedTokens > maxPromptTokens {
		var dropped bool
		prepared, dropped = dropOldestToolRound(prepared, historyEnd)
		if !dropped {
			break
		}
		degradations = append(degradations, "oldest tool round dropped")
		estimate = estimatePrompt(prepared, definitions)
		estimatedTokens = estimate.tokens(a.bytesPerToken)
	}
	for estimatedTokens > maxPromptTokens {
		var shrunk bool
		prepared, shrunk = shrinkLatestObservation(prepared, false)
		if !shrunk {
			break
		}
		degradations = append(degradations, "latest observation reduced")
		estimate = estimatePrompt(prepared, definitions)
		estimatedTokens = estimate.tokens(a.bytesPerToken)
	}
	if estimatedTokens > maxPromptTokens && historyEnd > 2 {
		prepared = compactAfterContextError(prepared, historyEnd)
		historyEnd = min(2, len(prepared))
		degradations = append(degradations, "old chat history dropped")
		estimate = estimatePrompt(prepared, definitions)
		estimatedTokens = estimate.tokens(a.bytesPerToken)
	}
	if estimatedTokens > maxPromptTokens {
		return preparedPrompt{
				Messages: prepared, HistoryEnd: historyEnd, Estimate: estimate,
				EstimatedTokens: estimatedTokens, MaxTokens: minCompletionTokens,
				Degradations: degradations,
			}, fmt.Errorf(
				"%w: estimated prompt %d exceeds safe budget %d",
				errContextExceeded, estimatedTokens, maxPromptTokens,
			)
	}
	return preparedPrompt{
		Messages: prepared, HistoryEnd: historyEnd, Estimate: estimate,
		EstimatedTokens: estimatedTokens,
		MaxTokens:       adaptiveMaxTokens(a.cfg.ContextTokens, estimatedTokens),
		Degradations:    degradations,
	}, nil
}

func (a *Agent) calibrateTokenEstimate(estimate promptEstimate, actualTokens int) {
	if actualTokens <= estimate.fixedTokens {
		return
	}
	observed := float64(estimate.textBytes) / float64(actualTokens-estimate.fixedTokens)
	observed = math.Max(2.0, math.Min(3.5, observed))
	a.bytesPerToken = a.bytesPerToken*0.75 + observed*0.25
}

func makeObservationMessage(
	call ToolCall,
	result ToolResult,
	limit int,
	lastHash *[sha256.Size]byte,
	haveLastHash *bool,
) PromptMessage {
	hashInput := make([]byte, 0, len(call.Function.Name)+len(result.Text)+len(result.ImageJPEG)+2)
	hashInput = append(hashInput, call.Function.Name...)
	hashInput = append(hashInput, 0)
	hashInput = append(hashInput, result.Text...)
	hashInput = append(hashInput, 0)
	hashInput = append(hashInput, result.ImageJPEG...)
	hash := sha256.Sum256(hashInput)
	if *haveLastHash && hash == *lastHash {
		return PromptMessage{
			Role:    "user",
			Content: "Observation from " + call.Function.Name + ":\n" + unchangedObservation,
		}
	}
	*lastHash = hash
	*haveLastHash = true
	text := truncatePromptText(result.Text, limit)
	if len(result.ImageJPEG) == 0 {
		return PromptMessage{
			Role: "user", Content: "Observation from " + call.Function.Name + ":\n" + text,
		}
	}
	return PromptMessage{
		Role: "user",
		Content: []map[string]any{
			{"type": "text", "text": text},
			{"type": "image_url", "image_url": map[string]string{
				"url": "data:image/jpeg;base64," + encodeBase64(result.ImageJPEG),
			}},
		},
	}
}

func (a *Agent) recordPromptAttempt(
	ctx context.Context,
	prepared preparedPrompt,
	usage tokenUsage,
	recovery string,
	err error,
) {
	degradations := append([]string(nil), prepared.Degradations...)
	if recovery != "" {
		degradations = append(degradations, recovery)
	}
	detail := fmt.Sprintf(
		"estimated_prompt=%d max_tokens=%d bytes_per_token=%.2f",
		prepared.EstimatedTokens, prepared.MaxTokens, a.bytesPerToken,
	)
	if len(degradations) > 0 {
		detail += " degradations=" + strings.Join(degradations, "; ")
	}
	if err != nil {
		detail += " error=" + err.Error()
	}
	log.Printf("agent context: %s prompt_n=%d predicted_n=%d", detail, usage.PromptTokens, usage.CompletionTokens)
	if a.cfg.Activity == nil {
		return
	}
	_ = a.cfg.Activity.Record(jobs.ActivityEvent{
		Kind: "llm", Name: "context-budget", JobID: jobs.JobID(ctx), Summary: "Agent prompt budget",
		Detail: jobs.ActivityDetail{
			Phase: "attempt", OK: err == nil, ResultText: detail,
			PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens,
		},
	})
}

func (a *Agent) attemptCompletion(
	ctx context.Context,
	messages []PromptMessage,
	historyEnd int,
	onDelta func(string),
	recovery string,
) (preparedPrompt, string, []ToolCall, tokenUsage, error) {
	prepared, err := a.preparePrompt(messages, historyEnd)
	if err != nil {
		a.recordPromptAttempt(ctx, prepared, tokenUsage{}, recovery, err)
		return prepared, "", nil, tokenUsage{}, err
	}
	reply, calls, usage, err := a.complete(ctx, prepared.Messages, prepared.MaxTokens, onDelta)
	a.recordPromptAttempt(ctx, prepared, usage, recovery, err)
	if usage.PromptTokens > 0 {
		a.calibrateTokenEstimate(prepared.Estimate, usage.PromptTokens)
	}
	return prepared, reply, calls, usage, err
}
