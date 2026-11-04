package main

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/valkey"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/config"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

const systemPoolMaxConns = 3

type poolConfigs struct {
	app      pgcommon.Config
	fallback pgcommon.Config
	system   pgcommon.Config
}

func newPoolConfigs(cfg *config.Config, log port.Logger) (poolConfigs, []pgcommon.ConfigWarning) {
	base, warnings := pgcommon.ConfigFromEnv()
	base.Logger = newPGCommonLogger(log)
	base.Tracer = gincommon.NewSpanTracer(cfg.OTELServiceName)

	app := base
	app.DSN = cfg.DatabaseURL
	app.GUCProvider = pgcommon.GUCSetFromContext

	fallback := app
	fallback.DSN = cfg.DatabaseFallbackURL
	fallback.PGBouncerMode = false

	system := base
	system.DSN = cfg.SystemDSN()
	system.MaxConns = systemPoolMaxConns
	system.MinConns = 0
	system.PoolName = "system"
	system.PGBouncerMode = true
	system.GUCProvider = nil

	return poolConfigs{app: app, fallback: fallback, system: system}, warnings
}

func newDBPool(ctx context.Context, pools poolConfigs, hasFallback bool) (*pgcommon.Pool, error) {
	pool, err := pgcommon.NewPool(ctx, pools.app)
	if err != nil && hasFallback {
		pool, err = pgcommon.NewPool(ctx, pools.fallback)
		if err != nil {
			return nil, fmt.Errorf("db pool (primary and fallback both failed): %w", err)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("db pool: %w", err)
	}
	return pool, nil
}

func newSystemDBPool(ctx context.Context, pools poolConfigs) (*pgcommon.Pool, error) {
	pool, err := pgcommon.NewPool(ctx, pools.system)
	if err != nil {
		return nil, fmt.Errorf("system db pool: %w", err)
	}
	return pool, nil
}

func newCacheStore(ctx context.Context, cfg *config.Config, log port.Logger) (port.CacheStore, *redis.Client) {
	client := redis.NewClient(&redis.Options{
		Addr:         cfg.ValkeyAddr,
		Password:     cfg.ValkeyPassword,
		DialTimeout:  cfg.ValkeyDialTimeout,
		ReadTimeout:  cfg.ValkeyReadTimeout,
		WriteTimeout: cfg.ValkeyWriteTimeout,
	})
	if err := client.Ping(ctx).Err(); err != nil {
		log.Warn("valkey unreachable at startup, serving without the cache until it returns", map[string]any{"error": err.Error()})
	}
	return valkey.NewCache(client), client
}
