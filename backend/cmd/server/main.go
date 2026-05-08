// Command server is the gym-check-in HTTP API entry point.
//
// At step 2 the server wires the cross-cutting middleware chain (request
// id → access logger → panic recovery → CORS → body size limit, plus
// HSTS in prod) and prepares the rate-limited /api/admin group. Auth
// handlers, member/membership routes, and the cron runner are added in
// subsequent steps; the entry point keeps HTTP timeouts and graceful
// shutdown so future steps inherit the right defaults.
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

	"github.com/lboyeon1223/gym-check-in/backend/internal/auth"
	"github.com/lboyeon1223/gym-check-in/backend/internal/cache"
	"github.com/lboyeon1223/gym-check-in/backend/internal/config"
	httpapi "github.com/lboyeon1223/gym-check-in/backend/internal/http"
	"github.com/lboyeon1223/gym-check-in/backend/internal/http/middleware"
	"github.com/lboyeon1223/gym-check-in/backend/internal/repo"
	"github.com/lboyeon1223/gym-check-in/backend/internal/util"
)

// authRateWindow / authRateMax mirror backend/CLAUDE.md's "IP 단위 rate
// limit (15분당 60회)" applied to authentication routes only.
//
// checkinLRUSize / checkinLRUTTL bound the in-process double-click cache for
// kiosk POST /api/check-ins. 1024 entries cover ≈100 active members per
// branch × tens of branches with headroom; the 5s TTL is fixed by
// backend/CLAUDE.md ("이중 클릭 방지(짧은 멱등성)... TTL 5초"). MVP single
// instance assumption — multi-instance deploy must move this to Redis.
const (
	authRateWindow = 15 * time.Minute
	authRateMax    = 60

	checkinLRUSize = 1024
	checkinLRUTTL  = 5 * time.Second
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

	logger := slog.Default()

	r := gin.New()
	// Middleware order matters — see internal/http/middleware/requestid.go
	// for the contract.  Each subsequent layer expects the upstream effects
	// (e.g. logger needs request_id, recovery wraps everything below it).
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger(logger))
	r.Use(middleware.Recovery(cfg.AppEnv, logger))
	r.Use(middleware.CORS(cfg.CORSOrigin))
	r.Use(middleware.BodyLimit(0)) // 1 MiB default
	r.Use(middleware.HSTS(cfg.AppEnv))

	// Trusted proxies — left empty until ADR-010 picks the hosting platform.
	// In dev there is no proxy in front of us; in prod we'll register the
	// platform's internal CIDRs once known.
	if err := r.SetTrustedProxies(nil); err != nil {
		return fmt.Errorf("set trusted proxies: %w", err)
	}

	// Healthz must remain reachable for platform health checks: it sits on
	// the engine directly so neither rate limit nor (future) auth applies.
	httpapi.RegisterHealth(r, pool)

	// JWT issuer — both secrets required once auth handlers are wired in.
	if cfg.JWTAccessSecret == "" || cfg.JWTRefreshSecret == "" {
		return fmt.Errorf("config: JWT_ACCESS_SECRET / JWT_REFRESH_SECRET required")
	}
	issuer := &auth.Issuer{
		AccessSecret:  []byte(cfg.JWTAccessSecret),
		RefreshSecret: []byte(cfg.JWTRefreshSecret),
		Clock:         util.SystemClock{},
		UUIDGen:       util.SystemUUIDGen{},
	}
	authHandlers := &httpapi.AuthHandlers{
		Pool:   pool,
		Issuer: issuer,
		Clock:  util.SystemClock{},
	}

	// Authentication route group — rate-limited per source IP. login/refresh
	// are public; logout/password require a valid access token (and bypass
	// MustChangePasswordGuard so first-login users can complete the change).
	rl := middleware.NewLimiter(authRateWindow, authRateMax, util.SystemClock{})
	publicAuth := r.Group("/api/admin")
	publicAuth.Use(rl.Middleware())
	publicAuth.POST("/login", authHandlers.Login)
	publicAuth.POST("/refresh", authHandlers.Refresh)

	protectedAuth := r.Group("/api/admin")
	protectedAuth.Use(rl.Middleware())
	protectedAuth.Use(middleware.RequireAuth(issuer, pool))
	protectedAuth.POST("/logout", authHandlers.Logout)
	protectedAuth.POST("/password", authHandlers.PasswordChange)

	// step 4 — admin/branches CRUD. Authenticated + must_change_password gate;
	// mutation routes additionally require role='global'.
	branchHandlers := &httpapi.BranchHandlers{Pool: pool}
	adminHandlers := &httpapi.AdminHandlers{Pool: pool}
	memberHandlers := &httpapi.MemberHandlers{Pool: pool}
	kioskHandlers := &httpapi.KioskHandlers{Pool: pool}

	// step 7 — kiosk check-ins (with LRU), admin check-in list, sales summary,
	// bulk extend. The LRU and the bulk-extend handler share util.SystemClock
	// so production timing matches the test wiring.
	checkinHandlers := &httpapi.CheckInHandlers{
		Pool:  pool,
		Cache: cache.NewLRU(checkinLRUSize, checkinLRUTTL, util.SystemClock{}),
		Clock: util.SystemClock{},
	}
	salesHandlers := &httpapi.SalesHandlers{Pool: pool}
	bulkHandlers := &httpapi.BulkExtendHandlers{Pool: pool, Clock: util.SystemClock{}}

	// step 5 — public (kiosk) routes share the same IP rate limit as the
	// auth group so a hostile client can't DoS the search endpoint either.
	// GET /api/branches is public because the kiosk needs the list before
	// any admin has logged in (initial 지점 선택 화면).
	publicAPI := r.Group("/api")
	publicAPI.Use(rl.Middleware())
	publicAPI.GET("/branches", branchHandlers.List)
	publicAPI.GET("/members/search", kioskHandlers.SearchMembers)
	publicAPI.GET("/check-ins/today-count", kioskHandlers.TodayCount)
	// step 7 — kiosk POST /check-ins is unauthenticated (tablet has no
	// admin session). The double-click LRU + DB SELECT … FOR UPDATE protect
	// against duplicate rows / concurrent decrement on pass10 quotas.
	publicAPI.POST("/check-ins", checkinHandlers.Create)

	api := r.Group("/api")
	api.Use(middleware.RequireAuth(issuer, pool))
	api.Use(middleware.MustChangePasswordGuard())

	// Member CRUD: branch admins are scoped to their own branch via
	// scopeFromContext inside the handlers; globals see everything.
	api.GET("/members", memberHandlers.List)
	api.GET("/members/:id", memberHandlers.GetByID)
	api.POST("/members", memberHandlers.Create)
	api.PATCH("/members/:id", memberHandlers.Update)
	api.DELETE("/members/:id", memberHandlers.Delete)

	// step 7 — admin check-in history (raw + daily aggregate). Branch
	// admins are scoped via scopeFromContext; globals optionally filter via
	// ?branchId=.
	api.GET("/check-ins", checkinHandlers.List)

	apiGlobal := api.Group("", middleware.RequireGlobal())
	apiGlobal.POST("/branches", branchHandlers.Create)
	apiGlobal.PATCH("/branches/:id", branchHandlers.Update)
	apiGlobal.DELETE("/branches/:id", branchHandlers.Delete)
	apiGlobal.GET("/admins", adminHandlers.List)
	apiGlobal.POST("/admins", adminHandlers.Create)
	apiGlobal.PATCH("/admins/:id", adminHandlers.Update)
	apiGlobal.DELETE("/admins/:id", adminHandlers.Delete)
	apiGlobal.POST("/admins/:id/reset-password", adminHandlers.ResetPassword)
	// step 7 — sales summary (gross/refund/net split) and bulk-extend.
	// Both are gated to role='global' and the bulk-extend handler enforces
	// Idempotency-Key validation internally.
	apiGlobal.GET("/sales/summary", salesHandlers.Summary)
	apiGlobal.POST("/memberships/bulk-extend", bulkHandlers.BulkExtend)

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
