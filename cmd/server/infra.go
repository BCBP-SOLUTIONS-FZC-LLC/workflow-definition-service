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

func newDBPool(ctx context.Context, cfg *config.Config) (*pgcommon.Pool, error) {
	// Logger is omitted: pgcommon.Config.Logger is typed as platform-pgcommon's
	// internal port.Logger, which uses unexported port.Field in its method
	// signatures. External consumers cannot implement that interface.
	// SlowQueryThreshold is still recorded; slow-query log lines will appear
	// once pgcommon exports its logger type.
	pool, err := pgcommon.NewPool(ctx, pgcommon.Config{
		DSN:                cfg.DatabaseURL,
		MaxConns:           cfg.PGMaxConns,
		MinConns:           cfg.PGMinConns,
		SlowQueryThreshold: time.Duration(cfg.PGSlowQueryThresholdMS) * time.Millisecond,
		GUCProvider:        pgcommon.GUCSetFromContext,
		PGBouncerMode:      cfg.PGBouncerMode,
	})
	if err != nil && cfg.DatabaseFallbackURL != "" {
		// PgBouncer may not be ready yet (rolling deploy, startup ordering).
		// Retry once against the direct Postgres fallback — startup only.
		pool, err = pgcommon.NewPool(ctx, pgcommon.Config{
			DSN:                cfg.DatabaseFallbackURL,
			MaxConns:           cfg.PGMaxConns,
			MinConns:           cfg.PGMinConns,
			SlowQueryThreshold: time.Duration(cfg.PGSlowQueryThresholdMS) * time.Millisecond,
			GUCProvider:        pgcommon.GUCSetFromContext,
			PGBouncerMode:      false,
		})
		if err != nil {
			return nil, fmt.Errorf("db pool (primary and fallback both failed): %w", err)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("db pool: %w", err)
	}
	return pool, nil
}

func newCacheStore(ctx context.Context, cfg *config.Config) (port.CacheStore, *redis.Client, error) {
	client := redis.NewClient(&redis.Options{
		Addr:         cfg.ValkeyAddr,
		Password:     cfg.ValkeyPassword,
		DialTimeout:  cfg.ValkeyDialTimeout,
		ReadTimeout:  cfg.ValkeyReadTimeout,
		WriteTimeout: cfg.ValkeyReadTimeout,
	})
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, nil, fmt.Errorf("valkey ping: %w", err)
	}
	return valkey.NewCache(client), client, nil
}

func newPublisher(cfg *config.Config, log port.Logger) (events.Publisher, error) {
	if cfg.AWSUseStub {
		return &mock.Publisher{}, nil
	}
	pub, err := events.NewSNSPublisher(events.SNSConfig{
		TopicARN:    cfg.SNSTopicARN,
		Region:      cfg.AWSRegion,
		EndpointURL: cfg.AWSEndpointURL,
		Logger:      log,
	})
	if err != nil {
		return nil, fmt.Errorf("sns publisher: %w", err)
	}
	return pub, nil
}

func newGlueCodec(ctx context.Context, cfg *config.Config) (port.GlueCodec, error) {
	var awsCfg aws.Config
	var err error
	if !cfg.AWSUseStub {
		awsCfg, err = awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.AWSRegion))
		if err != nil {
			return nil, fmt.Errorf("load aws config for glue: %w", err)
		}
	}
	return gluecodec.NewCodec(awsCfg, cfg.GlueRegistryName, cfg.AWSUseStub, cfg.AWSEndpointURL), nil
}
