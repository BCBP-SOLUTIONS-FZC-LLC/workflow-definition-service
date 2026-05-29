package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/logger"

	snsstub "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/sqs"
	sqsstub "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/sns"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/config"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

type app struct {
	cfg         *config.Config
	log         port.Logger
	pool        *pgxpool.Pool
	httpServer  *http.Server
	sqsConsumer *snsstub.StubConsumer
	shutdown    func()
}

func newApp(cfg *config.Config) (*app, error) {
	log, err := logger.NewLogger(cfg.AppEnv)
	if err != nil {
		return nil, fmt.Errorf("logger: %w", err)
	}

	tracingShutdown := gincommon.InitTracingFromEnv()

	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("db config: %w", err)
	}
	poolCfg.MaxConns = cfg.PGMaxConns
	poolCfg.MinConns = cfg.PGMinConns

	pool, err := pgxpool.NewWithConfig(context.Background(), poolCfg)
	if err != nil {
		tracingShutdown()
		return nil, fmt.Errorf("db pool: %w", err)
	}

	_ = sqsstub.NewStubPublisher(log)
	sqsConsumer := snsstub.NewStubConsumer(log)

	r := newRouter(cfg, pool, log)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.HTTPPort),
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return &app{
		cfg:         cfg,
		log:         log,
		pool:        pool,
		httpServer:  srv,
		sqsConsumer: sqsConsumer,
		shutdown:    tracingShutdown,
	}, nil
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
		if err := a.sqsConsumer.Run(ctx); err != nil {
			a.log.Error("SQS consumer exited", map[string]any{"error": err.Error()})
		}
	}()

	// TODO: start gRPC server
	// TODO: start outbox relay worker

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

	a.pool.Close()
	a.shutdown()

	a.log.Info("server stopped", nil)
}
