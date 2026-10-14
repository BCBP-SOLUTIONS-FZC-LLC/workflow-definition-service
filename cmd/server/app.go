package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	inboundhttp "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http"
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

func (a *app) run() error {
	fatal := make(chan error, 2)
	a.startServers(fatal)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	reason, failed := waitForStop(quit, fatal)

	a.log.Info("shutting down", map[string]any{"reason": reason})

	a.beginDrain()
	if !failed {
		time.Sleep(a.cfg.ShutdownDrainDelay)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), a.cfg.ShutdownGrace)
	defer shutdownCancel()
	a.stopServers(shutdownCtx)

	a.log.Info("server stopped", nil)
	if failed {
		return errors.New(reason)
	}
	return nil
}

func (a *app) startServers(fatal chan<- error) {
	exportCtx, cancel := context.WithCancel(context.Background())
	a.cancelExport = cancel
	goSafeRestart(exportCtx, "rls-exporter", a.log, time.Second, func() { a.rlsExporter.Run(exportCtx) })

	serve("http", a.httpServer, a.log, fatal)
	serve("metrics", a.metricsServer, a.log, fatal)
}

func (a *app) stopServers(ctx context.Context) {
	a.cancelExport()
	if err := a.httpServer.Shutdown(ctx); err != nil {
		a.log.Error("HTTP server shutdown error", map[string]any{"error": err.Error()})
	}
	if err := a.metricsServer.Shutdown(ctx); err != nil {
		a.log.Error("metrics server shutdown error", map[string]any{"error": err.Error()})
	}

	drainWithTimeout(a.log, "db pool", a.cfg.ShutdownPoolDrainTimeout, a.pool.DrainAndClose)
	drainWithTimeout(a.log, "system db pool", a.cfg.ShutdownPoolDrainTimeout, a.systemPool.DrainAndClose)
	if err := a.cacheClose.Close(); err != nil {
		a.log.Error("valkey close error", map[string]any{"error": err.Error()})
	}
	a.shutdown()
}

const httpWriteTimeoutMargin = 5 * time.Second

func newHTTPServer(port int, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      inboundhttp.RequestTimeout + httpWriteTimeoutMargin,
		IdleTimeout:       60 * time.Second,
	}
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
