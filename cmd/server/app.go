package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/pkg/events"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/pkg/outbox"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/config"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

type app struct {
	cfg            *config.Config
	log            port.Logger
	pool           *pgcommon.Pool
	cache          port.CacheStore
	httpServer     *http.Server
	grpcServer     *grpc.Server
	sqsConsumer    events.Consumer
	outboxRelay    *outbox.Runner
	eventPruner    *processedEventPruner
	shutdown       func()
	cacheClose     io.Closer
	executionClose io.Closer // nil when execution service is not configured
}

// processedEventPruner periodically deletes processed_event rows older than the configured TTL.
type processedEventPruner struct {
	repo     port.ProcessedEventRepository
	days     int
	interval time.Duration
	log      port.Logger
}

func (p *processedEventPruner) start(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := p.repo.PruneOlderThan(ctx, p.days); err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				p.log.Error("processed event prune failed", map[string]any{"error": err.Error()})
			}
		}
	}
}

func (a *app) run() {
	ctx, cancel := context.WithCancel(context.Background())
	a.startServers(ctx)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit

	a.log.Info("shutting down", map[string]any{"signal": sig.String()})
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	a.stopServers(shutdownCtx)

	a.log.Info("server stopped", nil)
}

func (a *app) startServers(ctx context.Context) {
	go func() {
		a.log.Info("HTTP server starting", map[string]any{"addr": a.httpServer.Addr})
		if err := a.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			a.log.Fatal("HTTP server error", map[string]any{"error": err.Error()})
		}
	}()

	go func() {
		addr := fmt.Sprintf(":%d", a.cfg.GRPCPort)
		lis, err := net.Listen("tcp", addr)
		if err != nil {
			a.log.Fatal("gRPC listen error", map[string]any{"error": err.Error()})
			return
		}
		a.log.Info("gRPC server starting", map[string]any{"addr": addr})
		if err := a.grpcServer.Serve(lis); err != nil {
			a.log.Error("gRPC server exited", map[string]any{"error": err.Error()})
		}
	}()

	go func() {
		a.log.Info("SQS consumer starting", map[string]any{"concurrency": a.cfg.SQSConcurrency})
		if err := a.sqsConsumer.Start(ctx); err != nil {
			a.log.Error("SQS consumer exited", map[string]any{"error": err.Error()})
		}
	}()

	go func() {
		a.log.Info("outbox relay starting", map[string]any{
			"poll_interval": a.cfg.OutboxPollInterval.String(),
			"batch_size":    a.cfg.OutboxBatchSize,
		})
		if err := a.outboxRelay.Start(ctx); err != nil {
			a.log.Error("outbox relay exited", map[string]any{"error": err.Error()})
		}
	}()

	go a.eventPruner.start(ctx)
}

func (a *app) stopServers(ctx context.Context) {
	if err := a.httpServer.Shutdown(ctx); err != nil {
		a.log.Error("HTTP server shutdown error", map[string]any{"error": err.Error()})
	}
	a.grpcServer.GracefulStop()

	if err := a.sqsConsumer.Stop(); err != nil {
		a.log.Error("SQS consumer stop error", map[string]any{"error": err.Error()})
	}
	if err := a.outboxRelay.Stop(); err != nil {
		a.log.Error("outbox relay stop error", map[string]any{"error": err.Error()})
	}

	if err := a.pool.DrainAndClose(ctx); err != nil {
		a.log.Warn("db pool drain timed out", map[string]any{"error": err.Error()})
	}
	if err := a.cacheClose.Close(); err != nil {
		a.log.Error("valkey close error", map[string]any{"error": err.Error()})
	}
	if a.executionClose != nil {
		if err := a.executionClose.Close(); err != nil {
			a.log.Error("execution client close error", map[string]any{"error": err.Error()})
		}
	}
	a.shutdown()
}
