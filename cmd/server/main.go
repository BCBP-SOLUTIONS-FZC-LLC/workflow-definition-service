package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/gincommon"

	snsstub "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/sqs"
	sqsstub "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/sns"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/config"
)

var version = "dev"

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	var zapLog *zap.Logger
	if cfg.AppEnv == "dev" {
		zapLog, err = zap.NewDevelopment()
	} else {
		zapLog, err = zap.NewProduction()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "logger init failed: %v\n", err)
		os.Exit(1)
	}
	defer zapLog.Sync() //nolint:errcheck

	shutdown := gincommon.InitTracingFromEnv()
	defer shutdown()

	_ = sqsstub.NewStubPublisher(zapLog)
	sqsConsumer := snsstub.NewStubConsumer(zapLog)

	if cfg.AppEnv != "dev" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.MaxMultipartMemory = 5 << 20 // 5MB payload cap

	mwCfg := gincommon.Config{
		Logger:       &portLogger{zapLog},
		ServiceName:  "workflow-definition-svc",
		BuildVersion: version,
	}
	for _, mw := range gincommon.ObservabilityMiddlewares(mwCfg) {
		r.Use(mw)
	}

	r.GET("/healthz", healthzHandler)
	r.GET("/readyz", readyzHandler)
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	api := r.Group("/api/v1")
	for _, mw := range gincommon.ProtectedMiddlewares(mwCfg) {
		api.Use(mw)
	}
	_ = api // TODO: register handlers in next phase

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.HTTPPort),
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		zapLog.Info("HTTP server starting", zap.String("addr", srv.Addr))
		if serveErr := srv.ListenAndServe(); serveErr != nil && serveErr != http.ErrServerClosed {
			zapLog.Fatal("HTTP server error", zap.Error(serveErr))
		}
	}()

	go func() {
		if runErr := sqsConsumer.Run(ctx); runErr != nil {
			zapLog.Error("SQS consumer exited", zap.Error(runErr))
		}
	}()

	// TODO: start gRPC server
	// TODO: start outbox relay worker

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit

	zapLog.Info("shutting down", zap.String("signal", sig.String()))
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	if err = srv.Shutdown(shutdownCtx); err != nil {
		zapLog.Error("HTTP server shutdown error", zap.Error(err))
	}

	zapLog.Info("server stopped")
}
