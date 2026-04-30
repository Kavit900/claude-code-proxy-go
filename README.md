# claude-code-proxy-go

Acts as a local proxy on `:8082` that intercepts Claude Code CLI requests and
forwards them to free LLM providers (NVIDIA NIM, OpenRouter, LM Studio, llama.cpp, DeepSeek).

```
Claude Code CLI / VSCode ext
        │  Anthropic API format (SSE)
        ▼
  localhost:8082   ← this proxy
        │  OpenAI-compatible format (SSE)
        ▼
  NVIDIA NIM / OpenRouter / LM Studio / llama.cpp
```

---

## Features

| Feature | Details |
|---|---|
| **Model routing** | Route Opus/Sonnet/Haiku to different providers/models |
| **SSE streaming** | Full server-sent event streaming, zero buffering |
| **Local optimisations** | Intercept title-gen, filepath probes, suggestion prompts locally |
| **Task tool intercept** | Forces `run_in_background=false` — no runaway subagents |
| **Rate limiting** | Proactive RPM throttle + reactive 429 exponential backoff |
| **Multi-provider** | NVIDIA NIM, OpenRouter, LM Studio, llama.cpp, DeepSeek |
| **Proxy support** | HTTP/SOCKS5 proxy for upstream requests |
| **Single binary** | No Python, no venv, no uv — just one static binary |

---

## Quick start

### 1. Prerequisites

- Go 1.22+
- A free API key from [NVIDIA NIM](https://build.nvidia.com) or [OpenRouter](https://openrouter.ai)

### 2. Install Claude Code CLI

```bash
npm install -g @anthropic-ai/claude-code
```

### 3. Clone & configure

```bash
git clone <this-repo>
cd claude-code-proxy-go
cp .env.example .env
# Edit .env — set your NVIDIA_NIM_API_KEY and MODEL_* vars
```

### 4. Run the proxy

```bash
make run
# or
go run ./cmd/server/main.go
```

### 5. Launch Claude Code

```bash
ANTHROPIC_BASE_URL="http://localhost:8082" \
ANTHROPIC_AUTH_TOKEN="sk-ant-fakekey123" \
claude
```

Or add a permanent alias to `~/.zshrc` / `~/.bashrc`:

```bash
alias claude='ANTHROPIC_BASE_URL="http://localhost:8082" ANTHROPIC_AUTH_TOKEN="sk-ant-fakekey123" claude'
```

or

```
ANTHROPIC_BASE_URL="http://localhost:8082" ANTHROPIC_AUTH_TOKEN="sk-ant-fakekey123" claude
```

---

## Provider configuration

### NVIDIA NIM (recommended — 40 req/min free)

```env
NVIDIA_NIM_API_KEY=nvapi-your-key-here
MODEL_OPUS=nvidia_nim/z-ai/glm4.7
MODEL_SONNET=nvidia_nim/moonshotai/kimi-k2-thinking
MODEL_HAIKU=nvidia_nim/stepfun-ai/step-3.5-flash
MODEL=nvidia_nim/z-ai/glm4.7
```

### OpenRouter (hundreds of free models)

```env
OPENROUTER_API_KEY=sk-or-your-key-here
MODEL_OPUS=open_router/deepseek/deepseek-r1-0528:free
MODEL_SONNET=open_router/openai/gpt-oss-120b:free
MODEL_HAIKU=open_router/stepfun/step-3.5-flash:free
MODEL=open_router/stepfun/step-3.5-flash:free
```

### LM Studio (fully local — no API key)

```env
MODEL=lmstudio/your-model-name
```

### llama.cpp (fully local — no API key)

```env
MODEL=llamacpp/your-model-name
```

---

## Build a static binary

```bash
make build
./claude-code-proxy-go
```

Cross-compile for Linux from macOS:

```bash
GOOS=linux GOARCH=amd64 go build -o claude-code-proxy-go-linux ./cmd/server
```

---

## CI/CD & Releases

### Automated CI Builds

Every push to `main` triggers a CI build that compiles for all platforms:

```bash
git push origin main
```

This creates temporary build artifacts that are stored for **7 days** to verify the build works correctly.

### Creating Releases

To create a permanent release with binaries attached:

```bash
# Create and push a tag
git tag v1.0.0
git push origin v1.0.0
```

The tag triggers the release workflow which:
- Builds binaries for Linux, macOS (Intel & Apple Silicon), and Windows (.exe)
- Creates a GitHub Release with all binaries attached
- Generates release notes automatically

**Release assets are permanent** and available on your repository's Releases page.

### Downloading Binaries

After a release is created, users can download from the Releases page or use:

```bash
# Linux
curl -L https://github.com/Kavit900/claude-code-proxy-go/releases/latest/download/claude-code-proxy-go-linux-amd64

# macOS Intel
curl -L https://github.com/Kavit900/claude-code-proxy-go/releases/latest/download/claude-code-proxy-go-darwin-amd64

# macOS Apple Silicon
curl -L https://github.com/Kavit900/claude-code-proxy-go/releases/latest/download/claude-code-proxy-go-darwin-arm64

# Windows (.exe)
curl -L https://github.com/Kavit900/claude-code-proxy-go/releases/latest/download/claude-code-proxy-go-windows-amd64.exe
```

---

## Project structure

```
claude-code-proxy-go/
├── cmd/server/main.go          # Entry point, HTTP server, graceful shutdown
├── internal/
│   ├── api/handler.go          # /v1/messages, /v1/models, /health routes
│   ├── config/config.go        # .env loading, model routing, provider config
│   ├── optimizations/          # Local intercepts (title-gen, filepath, Task tool)
│   ├── providers/
│   │   ├── provider.go         # HTTP client, rate limiting, retry/backoff
│   │   └── registry.go         # Provider factory + cache
│   └── proxy/
│       ├── converter.go        # Anthropic → OpenAI message conversion
│       └── stream.go           # SSE proxy, OAI → Anthropic stream conversion
└── pkg/anthropic/types.go      # Anthropic API types
```

---

## Environment variables

See `.env.example` for all supported variables with comments.

Key variables:

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8082` | Proxy listen port |
| `MODEL` | — | Fallback provider/model |
| `MODEL_OPUS` | — | Model for claude-opus-* requests |
| `MODEL_SONNET` | — | Model for claude-sonnet-* requests |
| `MODEL_HAIKU` | — | Model for claude-haiku-* requests |
| `NVIDIA_NIM_API_KEY` | — | NVIDIA NIM API key |
| `OPENROUTER_API_KEY` | — | OpenRouter API key |
| `ENABLE_OPTIMIZATIONS` | `true` | Intercept trivial requests locally |
| `ENABLE_THINKING` | `true` | Allow thinking/reasoning blocks |
| `RPM_LIMIT` | provider default | Requests per minute cap |
| `PROXY_URL` | — | HTTP/SOCKS5 proxy for upstream calls |
