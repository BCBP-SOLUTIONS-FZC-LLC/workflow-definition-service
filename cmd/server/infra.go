package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/pkg/events"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/pkg/events/mock"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/valkey"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/config"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

func newDBPool(ctx context.Context, cfg *config.Config) (*pgcommon.Pool, error) {
	pool, err := pgcommon.NewPool(ctx, pgcommon.Config{
		DSN:                cfg.DatabaseURL,
		MaxConns:           cfg.PGMaxConns,
		MinConns:           cfg.PGMinConns,
		SlowQueryThreshold: time.Duration(cfg.PGSlowQueryThresholdMS) * time.Millisecond,
		GUCProvider:        pgcommon.GUCSetFromContext,
	})
	if err != nil {
		return nil, fmt.Errorf("db pool: %w", err)
	}
	return pool, nil
}

func newCacheStore(ctx context.Context, cfg *config.Config) (port.CacheStore, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.ValkeyAddr,
		Password: cfg.ValkeyPassword,
	})
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("valkey ping: %w", err)
	}
	return valkey.NewCache(client), nil
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

func newConsumer(cfg *config.Config, log port.Logger, handler func(context.Context, events.Envelope[json.RawMessage]) error) (events.Consumer, error) {
	if cfg.AWSUseStub {
		c := &mock.Consumer{}
		c.SetHandler(handler)
		return c, nil
	}
	consumer, err := events.NewSQSConsumer(
		events.SQSConfig{
			QueueURL:    cfg.SQSQueueURL,
			Region:      cfg.AWSRegion,
			EndpointURL: cfg.AWSEndpointURL,
			Logger:      log,
		},
		handler,
		events.WithConcurrency(cfg.SQSConcurrency),
	)
	if err != nil {
		return nil, fmt.Errorf("sqs consumer: %w", err)
	}
	return consumer, nil
}
