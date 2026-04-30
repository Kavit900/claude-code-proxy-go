// Package optimizations intercepts known trivial Claude Code internal requests
// and responds locally without hitting any upstream provider, saving API quota.
//
// Ported from the Python project's api/optimizations.py logic.
package optimizations

import (
	"encoding/json"
	"strings"

	"github.com/free-claude-code-go/pkg/anthropic"
)

// Result is returned when a request is handled locally.
type Result struct {
	Handled  bool
	Response *anthropic.MessagesResponse
}

// Check inspects the request and returns a local response if it matches a
// known optimisation pattern.  Otherwise Handled=false is returned and the
// caller should forward to the upstream provider.
func Check(req *anthropic.MessagesRequest) Result {
	// 1. Title-generation probe (/title or similar tiny system-only messages)
	if isTitleGenRequest(req) {
		return Result{true, mockTextResponse(req.Model, "Untitled", "end_turn")}
	}

	// 2. Filepath / directory listing mock
	if isFilepathRequest(req) {
		return Result{true, mockTextResponse(req.Model, "[]", "end_turn")}
	}

	// 3. Suggestion skip – Claude Code asks "should I suggest X?" style prompts
	if isSuggestionProbe(req) {
		return Result{true, mockTextResponse(req.Model, "false", "end_turn")}
	}

	// 4. Prefix-detection (is user message a command prefix like /?)
	if isPrefixDetect(req) {
		return Result{true, mockTextResponse(req.Model, "false", "end_turn")}
	}

	// 5. Task tool run_in_background override – always return False to prevent runaway subagents
	if isTaskToolRequest(req) {
		return Result{true, taskToolResponse(req)}
	}

	return Result{Handled: false}
}

// ── Detection helpers ─────────────────────────────────────────────────────────

func lastUserText(req *anthropic.MessagesRequest) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		msg := req.Messages[i]
		if msg.Role != "user" {
			continue
		}
		switch v := msg.Content.(type) {
		case string:
			return strings.TrimSpace(v)
		case []interface{}:
			for _, block := range v {
				if m, ok := block.(map[string]interface{}); ok {
					if m["type"] == "text" {
						if t, ok := m["text"].(string); ok {
							return strings.TrimSpace(t)
						}
					}
				}
			}
		}
	}
	return ""
}

func systemText(req *anthropic.MessagesRequest) string {
	switch v := req.System.(type) {
	case string:
		return v
	case []interface{}:
		var sb strings.Builder
		for _, block := range v {
			if m, ok := block.(map[string]interface{}); ok {
				if t, ok := m["text"].(string); ok {
					sb.WriteString(t)
				}
			}
		}
		return sb.String()
	}
	return ""
}

var titleKeywords = []string{
	"generate a title", "create a title", "what is a good title",
	"suggest a title", "give me a title", "title for this conversation",
	"conversation title", "generate title",
}

func isTitleGenRequest(req *anthropic.MessagesRequest) bool {
	text := strings.ToLower(lastUserText(req) + " " + systemText(req))
	for _, kw := range titleKeywords {
		if strings.Contains(text, kw) {
			return true
		}
	}
	// Very short single-message with max_tokens ≤ 20 and no tools → likely title gen
	if len(req.Messages) == 1 && req.MaxTokens <= 20 && len(req.Tools) == 0 {
		return true
	}
	return false
}

var filepathKeywords = []string{
	"list files", "list the files", "show files", "directory listing",
	"what files", "find files", "locate files",
}

func isFilepathRequest(req *anthropic.MessagesRequest) bool {
	text := strings.ToLower(lastUserText(req))
	for _, kw := range filepathKeywords {
		if strings.Contains(text, kw) {
			return true
		}
	}
	return false
}

var suggestionKeywords = []string{
	"should i suggest", "would you like me to", "do you want me to suggest",
	"shall i suggest", "should i offer",
}

func isSuggestionProbe(req *anthropic.MessagesRequest) bool {
	text := strings.ToLower(lastUserText(req))
	for _, kw := range suggestionKeywords {
		if strings.Contains(text, kw) {
			return true
		}
	}
	return false
}

var prefixKeywords = []string{
	"does the message start with", "is this a command", "starts with /",
	"begins with /",
}

func isPrefixDetect(req *anthropic.MessagesRequest) bool {
	text := strings.ToLower(lastUserText(req))
	for _, kw := range prefixKeywords {
		if strings.Contains(text, kw) {
			return true
		}
	}
	return false
}

// isTaskToolRequest detects when Claude Code is about to invoke the Task tool
// with run_in_background=true and intercepts it.
func isTaskToolRequest(req *anthropic.MessagesRequest) bool {
	for _, tool := range req.Tools {
		if tool.Name == "Task" || tool.Name == "task" {
			return true
		}
	}
	return false
}

// ── Response builders ─────────────────────────────────────────────────────────

func mockTextResponse(model, text, stopReason string) *anthropic.MessagesResponse {
	return &anthropic.MessagesResponse{
		ID:   "opt-local-0001",
		Type: "message",
		Role: "assistant",
		Content: []anthropic.ContentBlock{
			{Type: "text", Text: text},
		},
		Model:      model,
		StopReason: stopReason,
		Usage:      anthropic.Usage{InputTokens: 0, OutputTokens: 1},
	}
}

// taskToolResponse rewrites the Task tool call to force run_in_background=false.
func taskToolResponse(req *anthropic.MessagesRequest) *anthropic.MessagesResponse {
	input, _ := json.Marshal(map[string]interface{}{
		"run_in_background": false,
	})
	return &anthropic.MessagesResponse{
		ID:   "opt-local-task-0001",
		Type: "message",
		Role: "assistant",
		Content: []anthropic.ContentBlock{
			{
				Type:  "tool_use",
				ID:    "task_intercepted",
				Name:  "Task",
				Input: json.RawMessage(input),
			},
		},
		Model:      req.Model,
		StopReason: "tool_use",
		Usage:      anthropic.Usage{InputTokens: 0, OutputTokens: 5},
	}
}
