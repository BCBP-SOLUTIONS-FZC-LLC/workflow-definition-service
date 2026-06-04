package main

import (
	"context"
	"fmt"
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
	cfg         *config.Config
	log         port.Logger
	pool        *pgcommon.Pool
	cache       port.CacheStore
	httpServer  *http.Server
	grpcServer  *grpc.Server
	sqsConsumer events.Consumer
	outboxRelay *outbox.Runner
	shutdown    func()
}

func (a *app) run() {
	ctx, cancel := context.WithCancel(context.Background())

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
		if err := a.sqsConsumer.Start(ctx); err != nil {
			a.log.Error("SQS consumer exited", map[string]any{"error": err.Error()})
		}
	}()

	go func() {
		if err := a.outboxRelay.Start(ctx); err != nil {
			a.log.Error("outbox relay exited", map[string]any{"error": err.Error()})
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit

	a.log.Info("shutting down", map[string]any{"signal": sig.String()})
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := a.httpServer.Shutdown(shutdownCtx); err != nil {
		a.log.Error("HTTP server shutdown error", map[string]any{"error": err.Error()})
	}
	a.grpcServer.GracefulStop()

	if err := a.sqsConsumer.Stop(); err != nil {
		a.log.Error("SQS consumer stop error", map[string]any{"error": err.Error()})
	}

	if err := a.outboxRelay.Stop(); err != nil {
		a.log.Error("outbox relay stop error", map[string]any{"error": err.Error()})
	}

	a.pool.Close()
	a.shutdown()

	a.log.Info("server stopped", nil)
}
