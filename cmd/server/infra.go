package main

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/valkey"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/config"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

func dbPoolConfig(cfg *config.Config, dsn string, pgBouncerMode bool, log port.Logger) pgcommon.Config {
	return pgcommon.Config{
		DSN:                dsn,
		MaxConns:           cfg.PGMaxConns,
		MinConns:           cfg.PGMinConns,
		SlowQueryThreshold: time.Duration(cfg.PGSlowQueryThresholdMS) * time.Millisecond,
		GUCProvider:        pgcommon.GUCSetFromContext,
		PGBouncerMode:      pgBouncerMode,
		Logger:             newPGCommonLogger(log),
		Tracer:             gincommon.NewSpanTracer(cfg.OTELServiceName),
	}
}

func newDBPool(ctx context.Context, cfg *config.Config, log port.Logger) (*pgcommon.Pool, error) {
	pool, err := pgcommon.NewPool(ctx, dbPoolConfig(cfg, cfg.DatabaseURL, cfg.PGBouncerMode, log))
	if err != nil && cfg.DatabaseFallbackURL != "" {
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

const systemPoolMaxConns = 3

func newSystemDBPool(ctx context.Context, cfg *config.Config, log port.Logger) (*pgcommon.Pool, error) {
	pool, err := pgcommon.NewPool(ctx, pgcommon.Config{
		DSN:                cfg.SystemDSN(),
		MaxConns:           systemPoolMaxConns,
		MinConns:           0,
		SlowQueryThreshold: time.Duration(cfg.PGSlowQueryThresholdMS) * time.Millisecond,
		PGBouncerMode:      cfg.PGBouncerMode,
		Logger:             newPGCommonLogger(log),
		Tracer:             gincommon.NewSpanTracer(cfg.OTELServiceName),
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
