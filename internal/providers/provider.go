package providers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// Provider sends requests to an OpenAI-compatible upstream.
type Provider struct {
	name    string
	baseURL string
	apiKey  string
	client  *http.Client

	// Rate limiting
	mu          sync.Mutex
	windowStart time.Time
	reqCount    int
	rpmLimit    int // 0 = unlimited
	maxRetries  int
}

// New creates a new Provider.
func New(name, baseURL, apiKey string, rpmLimit int, proxyURL string) *Provider {
	transport := &http.Transport{
		MaxIdleConns:    20,
		IdleConnTimeout: 90 * time.Second,
	}

	if proxyURL != "" {
		if u, err := url.Parse(proxyURL); err == nil {
			transport.Proxy = http.ProxyURL(u)
		}
	}

	return &Provider{
		name:        name,
		baseURL:     baseURL,
		apiKey:      apiKey,
		rpmLimit:    rpmLimit,
		maxRetries:  3,
		windowStart: time.Now(),
		client: &http.Client{
			Timeout:   10 * time.Minute,
			Transport: transport,
		},
	}
}

// Send sends a JSON body to the chat completions endpoint and returns the raw HTTP response.
// It handles proactive rate-limit throttling and reactive 429 exponential backoff.
func (p *Provider) Send(body interface{}, stream bool) (*http.Response, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	endpoint := p.baseURL + "/chat/completions"

	var lastErr error
	for attempt := 0; attempt <= p.maxRetries; attempt++ {
		// Proactive RPM throttle
		if p.rpmLimit > 0 {
			p.throttle()
		}

		req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(b))
		if err != nil {
			return nil, fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
		if stream {
			req.Header.Set("Accept", "text/event-stream")
		}

		resp, err := p.client.Do(req)
		if err != nil {
			lastErr = err
			wait := backoff(attempt)
			log.Printf("[%s] request error (attempt %d/%d): %v — retrying in %s", p.name, attempt+1, p.maxRetries+1, err, wait)
			time.Sleep(wait)
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			wait := backoff(attempt)
			log.Printf("[%s] 429 rate limited (attempt %d/%d) — retrying in %s", p.name, attempt+1, p.maxRetries+1, wait)
			time.Sleep(wait)
			lastErr = fmt.Errorf("429 rate limited")
			continue
		}

		return resp, nil
	}
	return nil, fmt.Errorf("all retries exhausted for provider %s: %w", p.name, lastErr)
}

// throttle sleeps if we are approaching the RPM limit within the current window.
func (p *Provider) throttle() {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	if now.Sub(p.windowStart) >= time.Minute {
		p.windowStart = now
		p.reqCount = 0
	}

	p.reqCount++
	if p.reqCount >= p.rpmLimit {
		remaining := time.Minute - now.Sub(p.windowStart)
		if remaining > 0 {
			log.Printf("[%s] proactive RPM throttle — sleeping %s", p.name, remaining.Round(time.Millisecond))
			p.mu.Unlock()
			time.Sleep(remaining)
			p.mu.Lock()
			p.windowStart = time.Now()
			p.reqCount = 1
		}
	}
}

func backoff(attempt int) time.Duration {
	base := time.Duration(1<<uint(attempt)) * time.Second
	if base > 30*time.Second {
		base = 30 * time.Second
	}
	return base
}
