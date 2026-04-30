package providers

import (
	"fmt"
	"sync"

	"github.com/free-claude-code-go/internal/config"
)

// Registry holds one Provider instance per provider name.
type Registry struct {
	mu        sync.Mutex
	providers map[string]*Provider
	cfg       *config.Config
}

func NewRegistry(cfg *config.Config) *Registry {
	return &Registry{
		providers: make(map[string]*Provider),
		cfg:       cfg,
	}
}

// Get returns (creating if needed) the Provider for the given name.
func (r *Registry) Get(name string) (*Provider, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if p, ok := r.providers[name]; ok {
		return p, nil
	}

	baseURL := r.cfg.BaseURLForProvider(name)
	if baseURL == "" {
		return nil, fmt.Errorf("unknown provider %q — set a base URL in .env", name)
	}

	apiKey := r.cfg.APIKeyForProvider(name)
	rpm := rpmForProvider(name, r.cfg)

	p := New(name, baseURL, apiKey, rpm, r.cfg.ProxyURL)
	r.providers[name] = p
	return p, nil
}

func rpmForProvider(name string, cfg *config.Config) int {
	if cfg.RPMLimit > 0 {
		return cfg.RPMLimit
	}
	// Sensible defaults per provider
	switch name {
	case "nvidia_nim":
		return 40
	case "open_router":
		return 20
	default:
		return 0
	}
}
