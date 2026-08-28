package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/pkg/outbox"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/config"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

type app struct {
	cfg            *config.Config
	log            port.Logger
	pool           *pgcommon.Pool
	systemPool     *pgcommon.Pool
	cache          port.CacheStore
	httpServer     *http.Server
	metricsServer  *http.Server
	grpcServer     *grpc.Server
	outboxRelay    *outbox.Runner
	shutdown       func()
	cacheClose     io.Closer
	executionClose io.Closer // nil when execution service is not configured
}

func (a *app) run() {
	a.startServers()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit

	a.log.Info("shutting down", map[string]any{"signal": sig.String()})

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	a.stopServers(shutdownCtx)

	a.log.Info("server stopped", nil)
}

func (a *app) startServers() {
	go func() {
		a.log.Info("HTTP server starting", map[string]any{"addr": a.httpServer.Addr})
		if err := a.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			a.log.Error("HTTP server error", map[string]any{"error": err.Error()})
			os.Exit(1)
		}
	}()

	go func() {
		a.log.Info("metrics server starting", map[string]any{"addr": a.metricsServer.Addr})
		if err := a.metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			a.log.Error("metrics server error", map[string]any{"error": err.Error()})
			os.Exit(1)
		}
	}()

	go func() {
		addr := fmt.Sprintf(":%d", a.cfg.GRPCPort)
		lis, err := net.Listen("tcp", addr)
		if err != nil {
			a.log.Error("gRPC listen error", map[string]any{"error": err.Error()})
			os.Exit(1)
		}
		a.log.Info("gRPC server starting", map[string]any{"addr": addr})
		if err := a.grpcServer.Serve(lis); err != nil {
			a.log.Error("gRPC server exited", map[string]any{"error": err.Error()})
		}
	}()

	go func() {
		a.log.Info("outbox relay starting", map[string]any{
			"poll_interval": a.cfg.OutboxPollInterval.String(),
			"batch_size":    a.cfg.OutboxBatchSize,
		})
		// Uses a fresh background context, not the shutdown ctx, so an in-flight
		// SNS publish isn't cancelled mid-flight; stopServers drives graceful stop instead.
		if err := a.outboxRelay.Start(context.Background()); err != nil {
			a.log.Error("outbox relay exited", map[string]any{"error": err.Error()})
		}
	}()
}

func (a *app) stopServers(ctx context.Context) {
	if err := a.httpServer.Shutdown(ctx); err != nil {
		a.log.Error("HTTP server shutdown error", map[string]any{"error": err.Error()})
	}
	if err := a.metricsServer.Shutdown(ctx); err != nil {
		a.log.Error("metrics server shutdown error", map[string]any{"error": err.Error()})
	}
	a.grpcServer.GracefulStop()

	if err := a.outboxRelay.Stop(); err != nil {
		a.log.Error("outbox relay stop error", map[string]any{"error": err.Error()})
	}

	if err := a.pool.DrainAndClose(ctx); err != nil {
		a.log.Warn("db pool drain timed out", map[string]any{"error": err.Error()})
	}
	if err := a.systemPool.DrainAndClose(ctx); err != nil {
		a.log.Warn("system db pool drain timed out", map[string]any{"error": err.Error()})
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
