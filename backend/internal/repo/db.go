// Package repo owns all SQL access. Higher layers must depend on repository
// interfaces only — handler/service code MUST NOT execute SQL directly.
package repo

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgxpool tunables — keep in sync with backend/CLAUDE.md.
const (
	poolMaxConns        = 25
	poolMinConns        = 2
	poolMaxConnIdleTime = 5 * time.Minute
	poolMaxConnLifetime = 1 * time.Hour
)

// NewPool builds a configured pgxpool with our standard tuning and forces
// every connection's session timezone to UTC. KST conversions are done
// explicitly via "AT TIME ZONE 'Asia/Seoul'" in SQL — never via the session
// timezone — so SQL behavior cannot vary by environment.
func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("repo: parse pool config: %w", err)
	}
	cfg.MaxConns = poolMaxConns
	cfg.MinConns = poolMinConns
	cfg.MaxConnIdleTime = poolMaxConnIdleTime
	cfg.MaxConnLifetime = poolMaxConnLifetime

	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		if _, err := conn.Exec(ctx, "set time zone 'UTC'"); err != nil {
			return fmt.Errorf("repo: set utc on new conn: %w", err)
		}
		return nil
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("repo: open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("repo: ping pool: %w", err)
	}
	return pool, nil
}
