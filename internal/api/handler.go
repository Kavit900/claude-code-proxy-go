package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/free-claude-code-go/internal/config"
	"github.com/free-claude-code-go/internal/optimizations"
	"github.com/free-claude-code-go/internal/providers"
	"github.com/free-claude-code-go/internal/proxy"
	"github.com/free-claude-code-go/pkg/anthropic"
)

// Handler holds all dependencies for the API layer.
type Handler struct {
	cfg      *config.Config
	registry *providers.Registry
}

func NewHandler(cfg *config.Config) *Handler {
	return &Handler{
		cfg:      cfg,
		registry: providers.NewRegistry(cfg),
	}
}

// RegisterRoutes wires up all HTTP routes.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/v1/messages", h.Messages)
	mux.HandleFunc("/v1/models", h.Models)
	mux.HandleFunc("/health", h.Health)
	// Claude Code also hits /v1/complete (legacy) — proxy it the same way
	mux.HandleFunc("/v1/complete", h.NotImplemented)
}

// ── /v1/messages ──────────────────────────────────────────────────────────────

func (h *Handler) Messages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	start := time.Now()

	// Parse request
	body, err := io.ReadAll(r.Body)
	if err != nil {
		proxy.WriteAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "cannot read body")
		return
	}

	var req anthropic.MessagesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		proxy.WriteAnthropicError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("invalid JSON: %v", err))
		return
	}

	log.Printf("→ model=%s stream=%v tokens=%d msgs=%d tools=%d",
		req.Model, req.Stream, req.MaxTokens, len(req.Messages), len(req.Tools))

	// ── 1. Local optimisation intercepts ─────────────────────────────────────
	if h.cfg.EnableOptimizations {
		if result := optimizations.Check(&req); result.Handled {
			log.Printf("  ⚡ intercepted locally in %s", time.Since(start))
			if req.Stream {
				h.streamLocalResponse(w, result.Response, req.Model)
			} else {
				writeJSON(w, http.StatusOK, result.Response)
			}
			return
		}
	}

	// ── 2. Model routing ─────────────────────────────────────────────────────
	providerModel, providerName := h.cfg.RouteModel(req.Model)
	log.Printf("  ↪ routed to provider=%s model=%s", providerName, providerModel)

	// ── 3. Convert to OpenAI format ──────────────────────────────────────────
	oaiReq := proxy.ConvertRequest(&req, providerModel, req.Stream)

	// ── 4. Strip thinking blocks if disabled ─────────────────────────────────
	if !h.cfg.EnableThinking {
		oaiReq = stripThinkingFromRequest(oaiReq)
	}

	// ── 5. Get provider and send ─────────────────────────────────────────────
	provider, err := h.registry.Get(providerName)
	if err != nil {
		proxy.WriteAnthropicError(w, http.StatusBadGateway, "api_error", err.Error())
		return
	}

	upstreamResp, err := provider.Send(oaiReq, req.Stream)
	if err != nil {
		proxy.WriteAnthropicError(w, http.StatusBadGateway, "api_error", fmt.Sprintf("upstream error: %v", err))
		return
	}

	// ── 6. Stream or non-stream response ─────────────────────────────────────
	if req.Stream {
		log.Printf("  ↩ streaming response [%s] provider=%s elapsed=%s", req.Model, providerName, time.Since(start))
		proxy.StreamProxy(w, upstreamResp, req.Model, h.cfg.EnableThinking)
	} else {
		defer upstreamResp.Body.Close()
		respBody, err := io.ReadAll(upstreamResp.Body)
		if err != nil {
			proxy.WriteAnthropicError(w, http.StatusBadGateway, "api_error", "failed to read upstream response")
			return
		}

		if upstreamResp.StatusCode != http.StatusOK {
			log.Printf("  ✗ upstream error %d: %s", upstreamResp.StatusCode, string(respBody))
			proxy.WriteAnthropicError(w, upstreamResp.StatusCode, "api_error",
				fmt.Sprintf("upstream returned %d: %s", upstreamResp.StatusCode, string(respBody)))
			return
		}

		converted, err := proxy.ConvertOAIResponse(respBody, req.Model)
		if err != nil {
			proxy.WriteAnthropicError(w, http.StatusBadGateway, "api_error", fmt.Sprintf("response conversion: %v", err))
			return
		}

		log.Printf("  ↩ response [%s] stop=%s tokens=%d elapsed=%s",
			req.Model, converted.StopReason, converted.Usage.OutputTokens, time.Since(start))
		writeJSON(w, http.StatusOK, converted)
	}
}

// streamLocalResponse emits an Anthropic SSE stream for a locally generated response.
func (h *Handler) streamLocalResponse(w http.ResponseWriter, resp *anthropic.MessagesResponse, model string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	sendSSEEvent := func(eventType string, data interface{}) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, b)
		flusher.Flush()
	}

	sendSSEEvent("message_start", map[string]interface{}{
		"type":    "message_start",
		"message": resp,
	})
	sendSSEEvent("ping", map[string]string{"type": "ping"})

	for i, block := range resp.Content {
		sendSSEEvent("content_block_start", map[string]interface{}{
			"type":          "content_block_start",
			"index":         i,
			"content_block": block,
		})
		if block.Type == "text" {
			sendSSEEvent("content_block_delta", map[string]interface{}{
				"type":  "content_block_delta",
				"index": i,
				"delta": map[string]string{"type": "text_delta", "text": block.Text},
			})
		}
		sendSSEEvent("content_block_stop", map[string]interface{}{
			"type":  "content_block_stop",
			"index": i,
		})
	}

	sendSSEEvent("message_delta", map[string]interface{}{
		"type":  "message_delta",
		"delta": map[string]string{"type": "message_delta", "stop_reason": resp.StopReason},
		"usage": map[string]int{"output_tokens": resp.Usage.OutputTokens},
	})
	sendSSEEvent("message_stop", map[string]string{"type": "message_stop"})
}

// ── /v1/models ────────────────────────────────────────────────────────────────

func (h *Handler) Models(w http.ResponseWriter, r *http.Request) {
	// Return the standard Claude model IDs so Claude Code recognises them
	models := map[string]interface{}{
		"object": "list",
		"data": []map[string]interface{}{
			{"id": "claude-opus-4-5", "object": "model", "created": 1700000000, "owned_by": "anthropic"},
			{"id": "claude-sonnet-4-5", "object": "model", "created": 1700000000, "owned_by": "anthropic"},
			{"id": "claude-haiku-4-5", "object": "model", "created": 1700000000, "owned_by": "anthropic"},
			{"id": "claude-3-5-sonnet-20241022", "object": "model", "created": 1700000000, "owned_by": "anthropic"},
			{"id": "claude-3-5-haiku-20241022", "object": "model", "created": 1700000000, "owned_by": "anthropic"},
			{"id": "claude-3-opus-20240229", "object": "model", "created": 1700000000, "owned_by": "anthropic"},
		},
	}
	writeJSON(w, http.StatusOK, models)
}

// ── /health ───────────────────────────────────────────────────────────────────

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) NotImplemented(w http.ResponseWriter, r *http.Request) {
	proxy.WriteAnthropicError(w, http.StatusNotImplemented, "not_implemented", "use /v1/messages")
}

// ── helpers ───────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func stripThinkingFromRequest(req *proxy.OAIRequest) *proxy.OAIRequest {
	// Nothing to strip at the OAI level; thinking is Anthropic-side only.
	// This is a no-op here but kept for future use.
	return req
}
