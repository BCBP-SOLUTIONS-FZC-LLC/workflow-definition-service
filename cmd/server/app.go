package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/config"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type app struct {
	cfg           *config.Config
	log           port.Logger
	pool          *pgcommon.Pool
	systemPool    *pgcommon.Pool
	cache         port.CacheStore
	httpServer    *http.Server
	metricsServer *http.Server
	rlsExporter   *service.RLSViolationExporter
	cancelExport  context.CancelFunc
	shutdown      func()
	beginDrain    func()
	cacheClose    io.Closer
}

func (a *app) run() {
	a.startServers()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit

	a.log.Info("shutting down", map[string]any{"signal": sig.String()})

	a.beginDrain()
	time.Sleep(a.cfg.ShutdownDrainDelay)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	a.stopServers(shutdownCtx)

	a.log.Info("server stopped", nil)
}

func (a *app) startServers() {
	exportCtx, cancel := context.WithCancel(context.Background())
	a.cancelExport = cancel
	go a.rlsExporter.Run(exportCtx)

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
}

func (a *app) stopServers(ctx context.Context) {
	a.cancelExport()
	if err := a.httpServer.Shutdown(ctx); err != nil {
		a.log.Error("HTTP server shutdown error", map[string]any{"error": err.Error()})
	}
	if err := a.metricsServer.Shutdown(ctx); err != nil {
		a.log.Error("metrics server shutdown error", map[string]any{"error": err.Error()})
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
	a.shutdown()
}

func newMetricsServer(port int, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}
