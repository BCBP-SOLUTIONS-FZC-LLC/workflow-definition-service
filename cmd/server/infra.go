package main

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/redis/go-redis/v9"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/pkg/events"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/pkg/events/mock"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgcommon"

	gluecodec "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/glue"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/valkey"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/config"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

// dbPoolConfig builds a pgcommon.Config for dsn. Logger is left unset:
// platform-pgcommon's Config.Logger needs its internal, unexported
// port.Logger type, which external packages cannot implement.
func dbPoolConfig(cfg *config.Config, dsn string, pgBouncerMode bool) pgcommon.Config {
	return pgcommon.Config{
		DSN:                dsn,
		MaxConns:           cfg.PGMaxConns,
		MinConns:           cfg.PGMinConns,
		SlowQueryThreshold: time.Duration(cfg.PGSlowQueryThresholdMS) * time.Millisecond,
		GUCProvider:        pgcommon.GUCSetFromContext,
		PGBouncerMode:      pgBouncerMode,
	}
}

func newDBPool(ctx context.Context, cfg *config.Config) (*pgcommon.Pool, error) {
	pool, err := pgcommon.NewPool(ctx, dbPoolConfig(cfg, cfg.DatabaseURL, cfg.PGBouncerMode))
	if err != nil && cfg.DatabaseFallbackURL != "" {
		// PgBouncer may not be ready yet (rolling deploy, startup ordering); retry once direct.
		pool, err = pgcommon.NewPool(ctx, dbPoolConfig(cfg, cfg.DatabaseFallbackURL, false))
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

func newSystemDBPool(ctx context.Context, cfg *config.Config) (*pgcommon.Pool, error) {
	pool, err := pgcommon.NewPool(ctx, pgcommon.Config{
		DSN:                cfg.SystemDSN(),
		MaxConns:           systemPoolMaxConns,
		MinConns:           0,
		SlowQueryThreshold: time.Duration(cfg.PGSlowQueryThresholdMS) * time.Millisecond,
		PGBouncerMode:      cfg.PGBouncerMode,
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

func newPublisher(cfg *config.Config, log port.Logger, codec events.Codec) (events.Publisher, error) {
	if cfg.AWSUseStub {
		return &mock.Publisher{}, nil
	}
	pub, err := events.NewSNSPublisher(events.SNSConfig{
		TopicARN:    cfg.SNSTopicARN,
		Region:      cfg.AWSRegion,
		EndpointURL: cfg.AWSEndpointURL,
		Logger:      log,
	}, events.WithCodec(codec))
	if err != nil {
		return nil, fmt.Errorf("sns publisher: %w", err)
	}
	return pub, nil
}

func newGlueCodec(ctx context.Context, cfg *config.Config) (events.Codec, error) {
	var awsCfg aws.Config
	var err error
	if !cfg.AWSUseStub {
		awsCfg, err = awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.AWSRegion))
		if err != nil {
			return nil, fmt.Errorf("load aws config for glue: %w", err)
		}
	}
	return gluecodec.NewCodec(awsCfg, cfg.GlueRegistryName, cfg.AWSUseStub, cfg.AWSEndpointURL, cfg.GlueSchemaCacheTTL), nil
}
