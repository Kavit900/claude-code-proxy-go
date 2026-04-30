package config

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	// Server
	Port string
	Host string

	// Model routing  (maps claude model names → provider/model strings)
	ModelOpus   string // overrides claude-3-opus-*
	ModelSonnet string // overrides claude-3-5-sonnet-* and claude-sonnet-*
	ModelHaiku  string // overrides claude-3-haiku-* and claude-haiku-*
	ModelFallback string // used when no specific routing matches

	// Provider API keys
	NvidiaAPIKey    string
	OpenRouterAPIKey string
	DeepSeekAPIKey  string

	// Provider base URLs (overridable)
	NvidiaBaseURL    string
	OpenRouterBaseURL string
	LMStudioBaseURL  string
	LlamaCppBaseURL  string

	// Proxy
	ProxyURL string

	// Rate limiting
	MaxConcurrent int
	RPMLimit      int // requests per minute

	// Optimisations (intercept cheap requests locally)
	EnableOptimizations bool
	EnableThinking      bool // global switch for reasoning / thinking blocks

	// Logging
	LogLevel string
}

func Load() *Config {
	// Try to load .env from current dir or ~/.config/free-claude-code/.env
	_ = godotenv.Load()
	home, _ := os.UserHomeDir()
	_ = godotenv.Load(home + "/.config/free-claude-code/.env")

	cfg := &Config{
		Port:                getEnv("PORT", "8082"),
		Host:                getEnv("HOST", "0.0.0.0"),
		ModelOpus:           getEnv("MODEL_OPUS", ""),
		ModelSonnet:         getEnv("MODEL_SONNET", ""),
		ModelHaiku:          getEnv("MODEL_HAIKU", ""),
		ModelFallback:       getEnv("MODEL", ""),
		NvidiaAPIKey:        getEnv("NVIDIA_NIM_API_KEY", ""),
		OpenRouterAPIKey:    getEnv("OPENROUTER_API_KEY", ""),
		DeepSeekAPIKey:      getEnv("DEEPSEEK_API_KEY", ""),
		NvidiaBaseURL:       getEnv("NVIDIA_BASE_URL", "https://integrate.api.nvidia.com/v1"),
		OpenRouterBaseURL:   getEnv("OPENROUTER_BASE_URL", "https://openrouter.ai/api/v1"),
		LMStudioBaseURL:     getEnv("LMSTUDIO_BASE_URL", "http://localhost:1234/v1"),
		LlamaCppBaseURL:     getEnv("LLAMACPP_BASE_URL", "http://localhost:8080/v1"),
		ProxyURL:            getEnv("PROXY_URL", ""),
		MaxConcurrent:       getEnvInt("MAX_CONCURRENT", 0),
		RPMLimit:            getEnvInt("RPM_LIMIT", 0),
		EnableOptimizations: getEnvBool("ENABLE_OPTIMIZATIONS", true),
		EnableThinking:      getEnvBool("ENABLE_THINKING", true),
		LogLevel:            getEnv("LOG_LEVEL", "info"),
	}

	cfg.validate()
	return cfg
}

func (c *Config) validate() {
	if c.ModelFallback == "" && c.ModelOpus == "" && c.ModelSonnet == "" && c.ModelHaiku == "" {
		log.Println("⚠  WARNING: No MODEL env vars set. Set MODEL or MODEL_OPUS/MODEL_SONNET/MODEL_HAIKU in .env")
	}
}

// RouteModel maps an incoming Anthropic model name to the configured provider/model string.
// Priority: specific MODEL_OPUS/SONNET/HAIKU → MODEL fallback → passthrough.
func (c *Config) RouteModel(requestedModel string) (providerModel string, provider string) {
	lower := strings.ToLower(requestedModel)

	var target string
	switch {
	case strings.Contains(lower, "opus"):
		target = firstNonEmpty(c.ModelOpus, c.ModelFallback)
	case strings.Contains(lower, "sonnet"):
		target = firstNonEmpty(c.ModelSonnet, c.ModelFallback)
	case strings.Contains(lower, "haiku"):
		target = firstNonEmpty(c.ModelHaiku, c.ModelFallback)
	default:
		target = c.ModelFallback
	}

	if target == "" {
		return requestedModel, "passthrough"
	}

	// Parse provider prefix: "nvidia_nim/model", "open_router/model", "lmstudio/model", "llamacpp/model", "deepseek/model"
	parts := strings.SplitN(target, "/", 2)
	if len(parts) == 1 {
		return target, "openai" // bare model name → generic openai-compat
	}

	prefix := strings.ToLower(parts[0])
	model := parts[1]

	switch prefix {
	case "nvidia_nim":
		return model, "nvidia_nim"
	case "open_router":
		return model, "open_router"
	case "lmstudio":
		return model, "lmstudio"
	case "llamacpp":
		return model, "llamacpp"
	case "deepseek":
		return model, "deepseek"
	default:
		return target, "openai"
	}
}

func (c *Config) BaseURLForProvider(provider string) string {
	switch provider {
	case "nvidia_nim":
		return c.NvidiaBaseURL
	case "open_router":
		return c.OpenRouterBaseURL
	case "lmstudio":
		return c.LMStudioBaseURL
	case "llamacpp":
		return c.LlamaCppBaseURL
	case "deepseek":
		return "https://api.deepseek.com/v1"
	default:
		return ""
	}
}

func (c *Config) APIKeyForProvider(provider string) string {
	switch provider {
	case "nvidia_nim":
		return c.NvidiaAPIKey
	case "open_router":
		return c.OpenRouterAPIKey
	case "deepseek":
		return c.DeepSeekAPIKey
	case "lmstudio", "llamacpp":
		return "lm-studio" // local, no real key needed
	default:
		return ""
	}
}

func (c *Config) Addr() string {
	return fmt.Sprintf("%s:%s", c.Host, c.Port)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}

func getEnvBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		b, err := strconv.ParseBool(v)
		if err == nil {
			return b
		}
	}
	return def
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
