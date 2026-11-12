package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/redis/go-redis/v9"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
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

func newValkeyOptions(cfg *config.Config) *redis.Options {
	return &redis.Options{
		Addr:         cfg.ValkeyAddr,
		Password:     cfg.ValkeyPassword,
		DialTimeout:  cfg.ValkeyDialTimeout,
		ReadTimeout:  cfg.ValkeyReadTimeout,
		WriteTimeout: cfg.ValkeyWriteTimeout,
	}
}

// newReadinessOptions is the main client without retries: the client default of
// three would multiply the dial timeout past the readiness probe's timeout.
func newReadinessOptions(cfg *config.Config) *redis.Options {
	opts := newValkeyOptions(cfg)
	opts.MaxRetries = -1
	return opts
}

type bothClients struct{ main, ready io.Closer }

func (c bothClients) Close() error { return errors.Join(c.main.Close(), c.ready.Close()) }

// newCacheStore builds the cache the services use (default retries) and a
// separate one-attempt cache for the startup and readiness pings.
func newCacheStore(ctx context.Context, cfg *config.Config, log port.Logger) (store port.CacheStore, ready *valkey.Cache, closer io.Closer) {
	client := redis.NewClient(newValkeyOptions(cfg))
	readyClient := redis.NewClient(newReadinessOptions(cfg))
	ready = valkey.NewCache(readyClient)
	if err := ready.Ping(ctx); err != nil {
		log.Warn("valkey unreachable at startup, serving without the cache until it returns", map[string]any{"error": err.Error()})
	}
	return valkey.NewCache(client), ready, bothClients{main: client, ready: readyClient}
}
