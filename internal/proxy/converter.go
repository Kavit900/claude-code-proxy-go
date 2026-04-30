// Package proxy handles Anthropic ↔ OpenAI-compatible message conversion
// and the core SSE streaming proxy logic.
package proxy

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/free-claude-code-go/pkg/anthropic"
)

// ── Anthropic → OpenAI ────────────────────────────────────────────────────────

type OAIMessage struct {
	Role       string        `json:"role"`
	Content    interface{}   `json:"content"` // string or []OAIContentPart
	ToolCallID string        `json:"tool_call_id,omitempty"`
	Name       string        `json:"name,omitempty"`
	ToolCalls  []OAIToolCall `json:"tool_calls,omitempty"`
}

type OAIContentPart struct {
	Type     string       `json:"type"`
	Text     string       `json:"text,omitempty"`
	ImageURL *OAIImageURL `json:"image_url,omitempty"`
}

type OAIImageURL struct {
	URL string `json:"url"`
}

type OAIToolCall struct {
	Index    int             `json:"index"`
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Function OAIFunctionCall `json:"function"`
}

type OAIFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type OAITool struct {
	Type     string      `json:"type"`
	Function OAIFunction `json:"function"`
}

type OAIFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type OAIRequest struct {
	Model       string         `json:"model"`
	Messages    []OAIMessage   `json:"messages"`
	MaxTokens   int            `json:"max_tokens,omitempty"`
	Temperature *float64       `json:"temperature,omitempty"`
	TopP        *float64       `json:"top_p,omitempty"`
	Stream      bool           `json:"stream"`
	Tools       []OAITool      `json:"tools,omitempty"`
	StreamOpts  *OAIStreamOpts `json:"stream_options,omitempty"`
}

type OAIStreamOpts struct {
	IncludeUsage bool `json:"include_usage"`
}

// ConvertRequest converts an Anthropic MessagesRequest to an OpenAI chat request.
func ConvertRequest(req *anthropic.MessagesRequest, targetModel string, stream bool) *OAIRequest {
	oai := &OAIRequest{
		Model:       targetModel,
		Stream:      stream,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		TopP:        req.TopP,
	}

	if stream {
		oai.StreamOpts = &OAIStreamOpts{IncludeUsage: true}
	}

	// System prompt → first system message
	if req.System != nil {
		sysText := extractSystemText(req.System)
		if sysText != "" {
			oai.Messages = append(oai.Messages, OAIMessage{Role: "system", Content: sysText})
		}
	}

	// Convert messages
	for _, msg := range req.Messages {
		converted := convertMessage(msg)
		oai.Messages = append(oai.Messages, converted...)
	}

	// Convert tools
	for _, t := range req.Tools {
		oai.Tools = append(oai.Tools, OAITool{
			Type: "function",
			Function: OAIFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}

	return oai
}

func extractSystemText(system interface{}) string {
	switch v := system.(type) {
	case string:
		return v
	case []interface{}:
		var parts []string
		for _, block := range v {
			if m, ok := block.(map[string]interface{}); ok {
				if t, ok := m["text"].(string); ok {
					parts = append(parts, t)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// convertMessage converts one Anthropic message into one or more OAI messages
// (tool_result blocks become separate "tool" role messages).
func convertMessage(msg anthropic.Message) []OAIMessage {
	switch v := msg.Content.(type) {
	case string:
		return []OAIMessage{{Role: msg.Role, Content: v}}

	case []interface{}:
		return convertContentBlocks(msg.Role, v)

	case json.RawMessage:
		var blocks []interface{}
		if err := json.Unmarshal(v, &blocks); err == nil {
			return convertContentBlocks(msg.Role, blocks)
		}
	}
	return []OAIMessage{{Role: msg.Role, Content: ""}}
}

func convertContentBlocks(role string, blocks []interface{}) []OAIMessage {
	var textParts []OAIContentPart
	var toolCalls []OAIToolCall
	var toolResults []OAIMessage

	for _, raw := range blocks {
		block, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		blockType, _ := block["type"].(string)

		switch blockType {
		case "text":
			text, _ := block["text"].(string)
			textParts = append(textParts, OAIContentPart{Type: "text", Text: text})

		case "thinking", "redacted_thinking":
			// Skip thinking blocks in the outgoing request — providers don't need them
			continue

		case "image":
			// Convert base64 image source
			if src, ok := block["source"].(map[string]interface{}); ok {
				mediaType, _ := src["media_type"].(string)
				data, _ := src["data"].(string)
				url := fmt.Sprintf("data:%s;base64,%s", mediaType, data)
				textParts = append(textParts, OAIContentPart{
					Type:     "image_url",
					ImageURL: &OAIImageURL{URL: url},
				})
			}

		case "tool_use":
			id, _ := block["id"].(string)
			name, _ := block["name"].(string)
			inputRaw, _ := json.Marshal(block["input"])
			toolCalls = append(toolCalls, OAIToolCall{
				ID:   id,
				Type: "function",
				Function: OAIFunctionCall{
					Name:      name,
					Arguments: string(inputRaw),
				},
			})

		case "tool_result":
			toolUseID, _ := block["tool_use_id"].(string)
			content := extractToolResultContent(block["content"])
			toolResults = append(toolResults, OAIMessage{
				Role:       "tool",
				Content:    content,
				ToolCallID: toolUseID,
			})
		}
	}

	var result []OAIMessage

	// Build the primary assistant/user message
	if len(toolCalls) > 0 {
		// Assistant message with tool calls
		msg := OAIMessage{Role: role, ToolCalls: toolCalls}
		if len(textParts) == 1 {
			msg.Content = textParts[0].Text
		} else if len(textParts) > 1 {
			msg.Content = textParts
		}
		result = append(result, msg)
	} else if len(textParts) == 1 {
		result = append(result, OAIMessage{Role: role, Content: textParts[0].Text})
	} else if len(textParts) > 1 {
		result = append(result, OAIMessage{Role: role, Content: textParts})
	}

	// Append tool results as separate messages
	result = append(result, toolResults...)
	return result
}

func extractToolResultContent(content interface{}) string {
	switch v := content.(type) {
	case string:
		return v
	case []interface{}:
		var parts []string
		for _, block := range v {
			if m, ok := block.(map[string]interface{}); ok {
				if t, ok := m["text"].(string); ok {
					parts = append(parts, t)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}
