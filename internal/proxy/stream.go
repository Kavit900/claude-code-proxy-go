package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/free-claude-code-go/pkg/anthropic"
)

// ── OpenAI SSE types ──────────────────────────────────────────────────────────

type OAIStreamChunk struct {
	ID      string      `json:"id"`
	Object  string      `json:"object"`
	Created int64       `json:"created"`
	Model   string      `json:"model"`
	Choices []OAIChoice `json:"choices"`
	Usage   *OAIUsage   `json:"usage"`
}

type OAIChoice struct {
	Index        int      `json:"index"`
	Delta        OAIDelta `json:"delta"`
	FinishReason *string  `json:"finish_reason"`
}

type OAIDelta struct {
	Role      string        `json:"role,omitempty"`
	Content   string        `json:"content,omitempty"`
	ToolCalls []OAIToolCall `json:"tool_calls,omitempty"`
}

type OAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ── Non-streaming OpenAI response ─────────────────────────────────────────────

type OAIResponse struct {
	ID      string      `json:"id"`
	Object  string      `json:"object"`
	Model   string      `json:"model"`
	Choices []OAIChoice `json:"choices"`
	Usage   *OAIUsage   `json:"usage"`
}

type OAIMessage2 struct {
	Role      string        `json:"role"`
	Content   string        `json:"content"`
	ToolCalls []OAIToolCall `json:"tool_calls,omitempty"`
}

// ── Streamer ──────────────────────────────────────────────────────────────────

// StreamProxy reads an upstream OpenAI-compatible SSE stream and re-emits it
// in Anthropic SSE format to the downstream ResponseWriter.
func StreamProxy(w http.ResponseWriter, upstream *http.Response, requestedModel string, enableThinking bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	msgID := fmt.Sprintf("msg_%d", time.Now().UnixNano())

	// Send message_start
	sendEvent(w, flusher, "message_start", &anthropic.StreamEvent{
		Type: "message_start",
		Message: &anthropic.MessagesResponse{
			ID:    msgID,
			Type:  "message",
			Role:  "assistant",
			Model: requestedModel,
			Usage: anthropic.Usage{InputTokens: 0, OutputTokens: 0},
		},
	})
	sendEvent(w, flusher, "ping", map[string]string{"type": "ping"})

	scanner := bufio.NewScanner(upstream.Body)
	defer upstream.Body.Close()

	blockIndex := 0
	blockOpen := false
	var toolCallAccumulators = map[int]*toolCallState{}
	var inputTokens, outputTokens int

	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var chunk OAIStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			log.Printf("SSE parse error: %v | raw: %s", err, data)
			continue
		}

		// Accumulate usage from final chunk
		if chunk.Usage != nil {
			inputTokens = chunk.Usage.PromptTokens
			outputTokens = chunk.Usage.CompletionTokens
		}

		for _, choice := range chunk.Choices {
			delta := choice.Delta

			// Text delta
			if delta.Content != "" {
				if !blockOpen {
					sendEvent(w, flusher, "content_block_start", &anthropic.StreamEvent{
						Type:         "content_block_start",
						Index:        blockIndex,
						ContentBlock: &anthropic.ContentBlock{Type: "text", Text: ""},
					})
					blockOpen = true
				}
				sendEvent(w, flusher, "content_block_delta", &anthropic.StreamEvent{
					Type:  "content_block_delta",
					Index: blockIndex,
					Delta: &anthropic.Delta{Type: "text_delta", Text: delta.Content},
				})
			}

			// Tool call deltas
			for _, tc := range delta.ToolCalls {
				tcIndex := tc.Index

				state, exists := toolCallAccumulators[tcIndex]
				if !exists {
					// New tool call block
					if blockOpen {
						sendEvent(w, flusher, "content_block_stop", &anthropic.StreamEvent{
							Type: "content_block_stop", Index: blockIndex,
						})
						blockIndex++
						blockOpen = false
					}
					state = &toolCallState{id: tc.ID, name: tc.Function.Name}
					toolCallAccumulators[tcIndex] = state

					sendEvent(w, flusher, "content_block_start", &anthropic.StreamEvent{
						Type:  "content_block_start",
						Index: blockIndex,
						ContentBlock: &anthropic.ContentBlock{
							Type: "tool_use",
							ID:   tc.ID,
							Name: tc.Function.Name,
						},
					})
				}

				if tc.Function.Name != "" && state.name == "" {
					state.name = tc.Function.Name
				}
				if tc.Function.Arguments != "" {
					state.args += tc.Function.Arguments
					sendEvent(w, flusher, "content_block_delta", &anthropic.StreamEvent{
						Type:  "content_block_delta",
						Index: blockIndex,
						Delta: &anthropic.Delta{Type: "input_json_delta", PartialJSON: tc.Function.Arguments},
					})
				}
			}

			// Finish
			if choice.FinishReason != nil {
				if blockOpen || len(toolCallAccumulators) > 0 {
					sendEvent(w, flusher, "content_block_stop", &anthropic.StreamEvent{
						Type: "content_block_stop", Index: blockIndex,
					})
					blockIndex++
					blockOpen = false
				}

				stopReason := finishReasonToAnthropic(*choice.FinishReason)
				sendEvent(w, flusher, "message_delta", &anthropic.StreamEvent{
					Type:  "message_delta",
					Delta: &anthropic.Delta{Type: "message_delta", StopReason: stopReason},
					Usage: &anthropic.Usage{OutputTokens: outputTokens},
				})
			}
		}
	}

	sendEvent(w, flusher, "message_stop", map[string]string{"type": "message_stop"})

	// Final usage
	_ = inputTokens
}

type toolCallState struct {
	id   string
	name string
	args string
}

// ── Non-streaming ─────────────────────────────────────────────────────────────

// ConvertOAIResponse converts a non-streaming OpenAI response to Anthropic format.
func ConvertOAIResponse(body []byte, requestedModel string) (*anthropic.MessagesResponse, error) {
	var oai OAIResponse
	if err := json.Unmarshal(body, &oai); err != nil {
		return nil, fmt.Errorf("parse oai response: %w", err)
	}

	resp := &anthropic.MessagesResponse{
		ID:    oai.ID,
		Type:  "message",
		Role:  "assistant",
		Model: requestedModel,
	}

	if oai.Usage != nil {
		resp.Usage = anthropic.Usage{
			InputTokens:  oai.Usage.PromptTokens,
			OutputTokens: oai.Usage.CompletionTokens,
		}
	}

	for _, choice := range oai.Choices {
		// We need the full message, not just delta — unmarshal separately
		var raw map[string]interface{}
		choiceJSON, _ := json.Marshal(choice)
		_ = json.Unmarshal(choiceJSON, &raw)

		msgRaw, _ := raw["message"]
		if msgRaw == nil {
			continue
		}

		msgBytes, _ := json.Marshal(msgRaw)
		var msg OAIMessage2
		_ = json.Unmarshal(msgBytes, &msg)

		if msg.Content != "" {
			resp.Content = append(resp.Content, anthropic.ContentBlock{
				Type: "text",
				Text: msg.Content,
			})
		}
		for _, tc := range msg.ToolCalls {
			var inputRaw json.RawMessage
			_ = json.Unmarshal([]byte(tc.Function.Arguments), &inputRaw)
			resp.Content = append(resp.Content, anthropic.ContentBlock{
				Type:  "tool_use",
				ID:    tc.ID,
				Name:  tc.Function.Name,
				Input: inputRaw,
			})
		}

		if choice.FinishReason != nil {
			resp.StopReason = finishReasonToAnthropic(*choice.FinishReason)
		}
	}

	return resp, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func finishReasonToAnthropic(reason string) string {
	switch reason {
	case "stop":
		return "end_turn"
	case "tool_calls", "function_call":
		return "tool_use"
	case "length":
		return "max_tokens"
	case "content_filter":
		return "stop_sequence"
	default:
		return "end_turn"
	}
}

func sendEvent(w http.ResponseWriter, f http.Flusher, eventType string, data interface{}) {
	b, err := json.Marshal(data)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, b)
	f.Flush()
}

// WriteAnthropicError writes a JSON error in Anthropic's error format.
func WriteAnthropicError(w http.ResponseWriter, status int, errType, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	apiErr := anthropic.APIError{Type: "error"}
	apiErr.Error.Type = errType
	apiErr.Error.Message = message
	b, _ := json.Marshal(apiErr)
	w.Write(b)
}

// ReadBody reads and re-attaches the body for logging/debugging.
func ReadBody(r io.ReadCloser) ([]byte, io.ReadCloser, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, nil, err
	}
	return b, io.NopCloser(bytes.NewReader(b)), nil
}
