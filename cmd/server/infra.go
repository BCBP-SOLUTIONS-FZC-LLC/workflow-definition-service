package main

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/valkey"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/config"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

// dbPoolConfig builds a pgcommon.Config for dsn. log is wrapped in
// pgCommonLogger (pglogger.go) so pgcommon's slow-query tracer logs through
// this service's own Zap-backed port.Logger instead of going unlogged.
func dbPoolConfig(cfg *config.Config, dsn string, pgBouncerMode bool, log port.Logger) pgcommon.Config {
	return pgcommon.Config{
		DSN:                dsn,
		MaxConns:           cfg.PGMaxConns,
		MinConns:           cfg.PGMinConns,
		SlowQueryThreshold: time.Duration(cfg.PGSlowQueryThresholdMS) * time.Millisecond,
		GUCProvider:        pgcommon.GUCSetFromContext,
		PGBouncerMode:      pgBouncerMode,
		Logger:             newPGCommonLogger(log),
	}
}

func newDBPool(ctx context.Context, cfg *config.Config, log port.Logger) (*pgcommon.Pool, error) {
	pool, err := pgcommon.NewPool(ctx, dbPoolConfig(cfg, cfg.DatabaseURL, cfg.PGBouncerMode, log))
	if err != nil && cfg.DatabaseFallbackURL != "" {
		// PgBouncer may not be ready yet (rolling deploy, startup ordering); retry once direct.
		pool, err = pgcommon.NewPool(ctx, dbPoolConfig(cfg, cfg.DatabaseFallbackURL, false, log))
		if err != nil {
			return nil, fmt.Errorf("db pool (primary and fallback both failed): %w", err)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("db pool: %w", err)
	}
	return pool, nil
}

// systemPoolMaxConns is small: this pool only serves low-volume, admin-only
// scope=global module/template writes.
const systemPoolMaxConns = 3

func newSystemDBPool(ctx context.Context, cfg *config.Config, log port.Logger) (*pgcommon.Pool, error) {
	pool, err := pgcommon.NewPool(ctx, pgcommon.Config{
		DSN:                cfg.SystemDSN(),
		MaxConns:           systemPoolMaxConns,
		MinConns:           0,
		SlowQueryThreshold: time.Duration(cfg.PGSlowQueryThresholdMS) * time.Millisecond,
		PGBouncerMode:      cfg.PGBouncerMode,
		Logger:             newPGCommonLogger(log),
	})
	if err != nil {
		return nil, fmt.Errorf("system db pool: %w", err)
	}
	return pool, nil
}

func newCacheStore(ctx context.Context, cfg *config.Config) (port.CacheStore, *redis.Client, error) {
	client := redis.NewClient(&redis.Options{
		Addr:         cfg.ValkeyAddr,
		Password:     cfg.ValkeyPassword,
		DialTimeout:  cfg.ValkeyDialTimeout,
		ReadTimeout:  cfg.ValkeyReadTimeout,
		WriteTimeout: cfg.ValkeyWriteTimeout,
	})
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, nil, fmt.Errorf("valkey ping: %w", err)
	}
	return valkey.NewCache(client), client, nil
}
