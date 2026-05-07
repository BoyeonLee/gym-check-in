// Command server is the gym-check-in HTTP API entry point.
//
// Scaffold scope (phase 2 step 1): only GET /api/healthz is wired. Routing,
// middleware (auth/audit/CORS/rate-limit/request-id/recovery), and the cron
// runner are added in subsequent steps. Even at this stage the entry point
// must already enforce HTTP timeouts and graceful shutdown so future steps
// inherit the right defaults.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	nethttp "net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lboyeon1223/gym-check-in/backend/internal/config"
	httpapi "github.com/lboyeon1223/gym-check-in/backend/internal/http"
	"github.com/lboyeon1223/gym-check-in/backend/internal/repo"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server exited with error", "error", err.Error())
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if cfg.AppEnv == "prod" {
		gin.SetMode(gin.ReleaseMode)
	}

	startupCtx, cancelStartup := context.WithTimeout(context.Background(), 15*time.Second)
	pool, err := repo.NewPool(startupCtx, cfg.DatabaseURL)
	cancelStartup()
	if err != nil {
		return fmt.Errorf("init db pool: %w", err)
	}

	r := gin.New()
	httpapi.RegisterHealth(r, pool)

	srv := &nethttp.Server{
		Addr:              ":" + cfg.Port,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Listen on a goroutine so the main routine can wait on signals.
	serveErr := make(chan error, 1)
	go func() {
		slog.Info("server listening", "port", cfg.Port, "env", cfg.AppEnv)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, nethttp.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serveErr:
		// Listener died on its own — clean up the pool and surface the error.
		pool.Close()
		return err
	case sig := <-stopCh:
		slog.Info("shutdown signal received", "signal", sig.String())
	}

	// Graceful shutdown: stop accepting new connections, wait up to 30s for
	// in-flight requests, then close the DB pool. Cron is registered ahead
	// of the HTTP server in later steps and must stop first; this scaffold
	// has no cron yet so steps (1) → (2) → (3) collapse into HTTP → DB.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("http shutdown error", "error", err.Error())
	}
	pool.Close()
	slog.Info("shutdown complete")
	return nil
}
