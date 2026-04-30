package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/free-claude-code-go/internal/api"
	"github.com/free-claude-code-go/internal/config"
)

func main() {
	cfg := config.Load()

	printBanner(cfg)

	mux := http.NewServeMux()
	handler := api.NewHandler(cfg)
	handler.RegisterRoutes(mux)

	// Middleware: request logging + CORS
	root := corsMiddleware(loggingMiddleware(mux))

	srv := &http.Server{
		Addr:         cfg.Addr(),
		Handler:      root,
		ReadTimeout:  5 * time.Minute,
		WriteTimeout: 15 * time.Minute,
		IdleTimeout:  2 * time.Minute,
	}

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	suspend := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	signal.Notify(suspend, syscall.SIGTSTP)

	go func() {
		log.Printf("🚀  free-claude-code proxy listening on %s", cfg.Addr())
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	// Wait for interrupt signal (Ctrl+C) or suspend (Ctrl+Z)
	select {
	case <-quit:
		log.Println("shutting down …")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		log.Println("bye 👋")
	case <-suspend:
		log.Println("\n⚠️  Got Ctrl+Z (suspend). Use Ctrl+C to gracefully shutdown!")
		log.Println("   To resume: type 'fg'")
		log.Println("   To kill:   type 'kill %1'")
		// Block forever to keep process suspended
		select {}
	}
}

func printBanner(cfg *config.Config) {
	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════════════╗")
	fmt.Println("║       free-claude-code  (Go edition)             ║")
	fmt.Println("╠══════════════════════════════════════════════════╣")
	fmt.Printf ("║  Proxy addr  : http://%s%-18s║\n", cfg.Addr(), "")
	fmt.Printf ("║  Optimisations: %-32v ║\n", cfg.EnableOptimizations)
	fmt.Printf ("║  Thinking    : %-32v ║\n", cfg.EnableThinking)
	if cfg.ModelFallback != "" {
		fmt.Printf("║  Fallback    : %-32s ║\n", cfg.ModelFallback)
	}
	if cfg.ModelOpus != "" {
		fmt.Printf("║  Opus  →     : %-32s ║\n", cfg.ModelOpus)
	}
	if cfg.ModelSonnet != "" {
		fmt.Printf("║  Sonnet →    : %-32s ║\n", cfg.ModelSonnet)
	}
	if cfg.ModelHaiku != "" {
		fmt.Printf("║  Haiku  →    : %-32s ║\n", cfg.ModelHaiku)
	}
	fmt.Println("╚══════════════════════════════════════════════════╝")
	fmt.Println()
	fmt.Println("  Set these env vars for Claude Code:")
	fmt.Printf("  export ANTHROPIC_BASE_URL=http://localhost:%s\n", cfg.Port)
	fmt.Println("  export ANTHROPIC_AUTH_TOKEN=sk-ant-fakekey123")
	fmt.Println()
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, x-api-key, anthropic-version, anthropic-beta")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
