package anthropic

import "encoding/json"

// ── Shared ────────────────────────────────────────────────────────────────────

type ContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   interface{}     `json:"content,omitempty"`

	// Thinking block fields
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
}

type Message struct {
	Role    string        `json:"role"`
	Content interface{}   `json:"content"` // string or []ContentBlock
}

// ── Request ───────────────────────────────────────────────────────────────────

type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

type ThinkingConfig struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens,omitempty"`
}

type MessagesRequest struct {
	Model         string          `json:"model"`
	MaxTokens     int             `json:"max_tokens"`
	Messages      []Message       `json:"messages"`
	System        interface{}     `json:"system,omitempty"` // string or []ContentBlock
	Tools         []Tool          `json:"tools,omitempty"`
	Stream        bool            `json:"stream,omitempty"`
	Temperature   *float64        `json:"temperature,omitempty"`
	TopP          *float64        `json:"top_p,omitempty"`
	TopK          *int            `json:"top_k,omitempty"`
	StopSequences []string        `json:"stop_sequences,omitempty"`
	Metadata      json.RawMessage `json:"metadata,omitempty"`
	Thinking      *ThinkingConfig `json:"thinking,omitempty"`
}

// ── Response ──────────────────────────────────────────────────────────────────

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type MessagesResponse struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	Role         string         `json:"role"`
	Content      []ContentBlock `json:"content"`
	Model        string         `json:"model"`
	StopReason   string         `json:"stop_reason"`
	StopSequence *string        `json:"stop_sequence"`
	Usage        Usage          `json:"usage"`
}

// ── SSE Events ────────────────────────────────────────────────────────────────

type StreamEvent struct {
	Type  string      `json:"type"`
	Index int         `json:"index,omitempty"`
	Delta *Delta      `json:"delta,omitempty"`
	Usage *Usage      `json:"usage,omitempty"`

	// message_start
	Message *MessagesResponse `json:"message,omitempty"`

	// content_block_start
	ContentBlock *ContentBlock `json:"content_block,omitempty"`
}

type Delta struct {
	Type        string `json:"type,omitempty"`
	Text        string `json:"text,omitempty"`
	StopReason  string `json:"stop_reason,omitempty"`
	Thinking    string `json:"thinking,omitempty"`
	Signature   string `json:"signature,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
}

// ── Error ─────────────────────────────────────────────────────────────────────

type APIError struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}
