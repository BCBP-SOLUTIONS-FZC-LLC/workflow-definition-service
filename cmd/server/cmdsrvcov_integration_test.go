//go:build integration

package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/config"
)

const (
	cmdSrvCovWait        = 30 * time.Second
	cmdSrvCovUnreachable = "postgres://u:cmdsrv-secret-pw@127.0.0.1:1/wf?sslmode=disable"
)

// cmdSrvCovRunnable is an app built by newApp on the shared container whose two
// servers listen on loopback ports chosen by the kernel. The address of each
// listener is published once the server is really accepting connections.
type cmdSrvCovRunnable struct {
	app         *app
	rec         *cmdSrvRecorder
	httpAddr    chan string
	metricsAddr chan string
	drains      *atomic.Int32
	shutdowns   *atomic.Int32
}

func cmdSrvCovAddrReporter(addr chan<- string) func(net.Listener) context.Context {
	return func(l net.Listener) context.Context {
		select {
		case addr <- l.Addr().String():
		default:
		}
		return context.Background()
	}
}

func cmdSrvCovBuild(t *testing.T) *cmdSrvCovRunnable {
	t.Helper()
	superDSN := cmdSrvSuperDSN(t)
	_, roleDSN := cmdSrvAppRole(t, superDSN)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "none")
	t.Setenv("LOG_FILE", filepath.Join(t.TempDir(), "app.log"))
	cfg := cmdSrvAppConfig(roleDSN)
	cfg.ShutdownDrainDelay = 0
	cfg.ShutdownGrace = cmdSrvCovWait
	cfg.ShutdownPoolDrainTimeout = cmdSrvCovWait

	a, err := newApp(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		// Closing again is harmless and covers a test that failed before run stopped the app.
		a.pool.Close()
		a.systemPool.Close()
	})

	r := &cmdSrvCovRunnable{
		app:         a,
		rec:         &cmdSrvRecorder{},
		httpAddr:    make(chan string, 1),
		metricsAddr: make(chan string, 1),
		drains:      &atomic.Int32{},
		shutdowns:   &atomic.Int32{},
	}
	a.log = r.rec
	a.httpServer.Addr = "127.0.0.1:0"
	a.httpServer.BaseContext = cmdSrvCovAddrReporter(r.httpAddr)
	a.metricsServer.Addr = "127.0.0.1:0"
	a.metricsServer.BaseContext = cmdSrvCovAddrReporter(r.metricsAddr)
	drain, shutdown := a.beginDrain, a.shutdown
	a.beginDrain = func() { r.drains.Add(1); drain() }
	a.shutdown = func() { r.shutdowns.Add(1); shutdown() }
	return r
}

func cmdSrvCovAwait(t *testing.T, what string, ch <-chan string) string {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(cmdSrvCovWait):
		t.Fatalf("timed out waiting for %s", what)
		return ""
	}
}

func cmdSrvCovGet(t *testing.T, url string) (int, string) {
	t.Helper()
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := client.Get(url)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(body)
}

func cmdSrvCovLogIndex(r *cmdSrvRecorder, level, msg string) int {
	for i, e := range r.snapshot() {
		if e.level == level && e.msg == msg {
			return i
		}
	}
	return -1
}

func cmdSrvCovAssertStopped(t *testing.T, r *cmdSrvCovRunnable) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	assert.False(t, r.app.pool.Health(ctx).Healthy, "the application pool must be closed")
	assert.False(t, r.app.systemPool.Health(ctx).Healthy, "the system pool must be closed")
	assert.Error(t, r.app.cacheClose.Close(), "the cache client must already be closed")
	assert.Equal(t, int32(1), r.drains.Load(), "readiness must flip to draining exactly once")
	assert.Equal(t, int32(1), r.shutdowns.Load(), "tracing must be flushed exactly once")
}

func TestApp_Run_ServesUntilSIGTERMThenStopsEverything(t *testing.T) {
	r := cmdSrvCovBuild(t)

	// The test process must survive the signal whatever run does with it.
	guard := make(chan os.Signal, 4)
	signal.Notify(guard, syscall.SIGTERM)
	t.Cleanup(func() { signal.Stop(guard) })

	done := make(chan error, 1)
	go func() { done <- r.app.run() }()

	httpAddr := cmdSrvCovAwait(t, "the HTTP server to listen", r.httpAddr)
	metricsAddr := cmdSrvCovAwait(t, "the metrics server to listen", r.metricsAddr)
	code, _ := cmdSrvCovGet(t, "http://"+httpAddr+"/healthz")
	assert.Equal(t, http.StatusOK, code)
	code, body := cmdSrvCovGet(t, "http://"+metricsAddr+"/metrics")
	assert.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "# HELP")

	// run registers its own handler after starting the servers, so a signal sent
	// too early would be lost: keep sending until run reports that it returned.
	var runErr error
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	deadline := time.After(cmdSrvCovWait)
waiting:
	for {
		require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGTERM))
		select {
		case runErr = <-done:
			break waiting
		case <-tick.C:
		case <-deadline:
			t.Fatal("run did not return after SIGTERM")
		}
	}

	require.NoError(t, runErr, "a signal is a clean stop")
	shutting, ok := r.rec.find("info", "shutting down")
	require.True(t, ok)
	assert.Equal(t, syscall.SIGTERM.String(), shutting.fields["reason"])
	stopped := cmdSrvCovLogIndex(r.rec, "info", "server stopped")
	require.GreaterOrEqual(t, stopped, 0, "the final log line must be written")
	assert.Less(t, cmdSrvCovLogIndex(r.rec, "info", "shutting down"), stopped)
	for _, e := range r.rec.snapshot() {
		assert.NotContains(t, []string{"warn", "error"}, e.level, "unexpected %s entry %q", e.level, e.msg)
	}
	cmdSrvCovAssertStopped(t, r)

	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	for _, addr := range []string{httpAddr, metricsAddr} {
		resp, err := client.Get("http://" + addr + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
		}
		assert.Error(t, err, "%s must no longer accept connections", addr)
	}
}

func TestApp_Run_AServerThatCannotListenStopsTheServiceWithAnError(t *testing.T) {
	r := cmdSrvCovBuild(t)
	held, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = held.Close() })
	r.app.httpServer.Addr = held.Addr().String()

	guard := make(chan os.Signal, 1)
	signal.Notify(guard, syscall.SIGTERM)
	t.Cleanup(func() { signal.Stop(guard) })

	done := make(chan error, 1)
	go func() { done <- r.app.run() }()

	var runErr error
	select {
	case runErr = <-done:
	case <-time.After(cmdSrvCovWait):
		t.Fatal("run did not return although the HTTP port is taken")
	}

	require.Error(t, runErr)
	assert.True(t, strings.HasPrefix(runErr.Error(), "http server: "), "error = %q", runErr)
	assert.Contains(t, runErr.Error(), "address already in use")
	assert.Contains(t, runErr.Error(), held.Addr().String())
	shutting, ok := r.rec.find("info", "shutting down")
	require.True(t, ok)
	assert.Equal(t, runErr.Error(), shutting.fields["reason"], "the log carries the same reason run returns")
	_, stopped := r.rec.find("info", "server stopped")
	assert.True(t, stopped, "a failed start still stops the rest cleanly")
	cmdSrvCovAssertStopped(t, r)
}

// cmdSrvCovBlockedServer serves on a kernel-chosen loopback port and holds one
// request open until release is closed, so that Shutdown has a busy connection
// to wait for.
func cmdSrvCovBlockedServer(t *testing.T, release <-chan struct{}) *http.Server {
	t.Helper()
	entered := make(chan struct{}, 1)
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-release
		}),
		ReadHeaderTimeout: time.Second,
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = srv.Serve(ln) }()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
		resp, err := client.Get("http://" + ln.Addr().String() + "/")
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
	t.Cleanup(func() {
		_ = srv.Close()
		wg.Wait()
	})
	select {
	case <-entered:
	case <-time.After(cmdSrvCovWait):
		t.Fatal("the blocking request never reached the handler")
	}
	return srv
}

type cmdSrvCovFailingCloser struct{ err error }

func (c cmdSrvCovFailingCloser) Close() error { return c.err }

func TestApp_StopServers_ReportsEachFailureAndStillReleasesTheRest(t *testing.T) {
	dsn := cmdSrvSuperDSN(t)
	ctx := context.Background()
	pool, err := pgcommon.NewPool(ctx, pgcommon.Config{DSN: dsn})
	require.NoError(t, err)
	systemPool, err := pgcommon.NewPool(ctx, pgcommon.Config{DSN: dsn})
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	t.Cleanup(systemPool.Close)

	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	rec := &cmdSrvRecorder{}
	var exportCancelled, shutdowns atomic.Int32
	closeErr := errors.New("valkey went away")
	a := &app{
		cfg:           &config.Config{ShutdownPoolDrainTimeout: cmdSrvCovWait},
		log:           rec,
		pool:          pool,
		systemPool:    systemPool,
		httpServer:    cmdSrvCovBlockedServer(t, release),
		metricsServer: cmdSrvCovBlockedServer(t, release),
		cancelExport:  func() { exportCancelled.Add(1) },
		cacheClose:    cmdSrvCovFailingCloser{err: closeErr},
		shutdown:      func() { shutdowns.Add(1) },
	}
	expired, cancel := context.WithCancel(ctx)
	cancel()

	a.stopServers(expired)

	for _, msg := range []string{"HTTP server shutdown error", "metrics server shutdown error"} {
		e, ok := rec.find("error", msg)
		require.True(t, ok, "missing log line %q", msg)
		assert.Equal(t, context.Canceled.Error(), e.fields["error"])
	}
	e, ok := rec.find("error", "valkey close error")
	require.True(t, ok)
	assert.Equal(t, closeErr.Error(), e.fields["error"])
	assert.Equal(t, int32(1), exportCancelled.Load())
	assert.Equal(t, int32(1), shutdowns.Load(), "tracing is flushed even after earlier failures")
	check, cancelCheck := context.WithTimeout(ctx, 5*time.Second)
	defer cancelCheck()
	assert.False(t, pool.Health(check).Healthy)
	assert.False(t, systemPool.Health(check).Healthy)
}

func TestNewSystemDBPool_ConnectFailureIsPrefixedAndRedacted(t *testing.T) {
	pools := poolConfigs{system: pgcommon.Config{DSN: cmdSrvCovUnreachable}}

	pool, err := newSystemDBPool(context.Background(), pools)

	require.Error(t, err)
	assert.Nil(t, pool)
	assert.True(t, strings.HasPrefix(err.Error(), "system db pool: "), "error = %q", err)
	assert.NotContains(t, err.Error(), "cmdsrv-secret-pw")
}

func TestNewSystemDBPool_UsesTheSystemConfigNotTheApplicationOne(t *testing.T) {
	dsn := cmdSrvSuperDSN(t)
	pools := poolConfigs{
		app:    pgcommon.Config{DSN: cmdSrvCovUnreachable},
		system: pgcommon.Config{DSN: dsn, MaxConns: 2},
	}

	pool, err := newSystemDBPool(context.Background(), pools)

	require.NoError(t, err)
	t.Cleanup(pool.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got := dbPinger{pool: pool}.Health(ctx)
	assert.True(t, got.Healthy)
	assert.Equal(t, int32(2), got.MaxConns)
}

func TestNewApp_InitFailuresAfterTheApplicationPoolIsOpen(t *testing.T) {
	superDSN := cmdSrvSuperDSN(t)
	_, roleDSN := cmdSrvAppRole(t, superDSN)

	cases := []struct {
		name       string
		appDSN     string
		systemDSN  string
		wantPrefix string
		wantIn     string
	}{
		{"an unreachable system database is named as the system pool", roleDSN, cmdSrvCovUnreachable, "system db pool: ", ""},
		{"a superuser application role is refused", superDSN, "", "the application database role ", "bypasses row-level security"},
		{"a system role that cannot bypass row-level security is refused when SYSTEM_DATABASE_URL is set", roleDSN, roleDSN, "the system database role ", "SYSTEM_DATABASE_URL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "none")
			t.Setenv("LOG_FILE", filepath.Join(t.TempDir(), "app.log"))
			cfg := cmdSrvAppConfig(tc.appDSN)
			cfg.SystemDatabaseURL = tc.systemDSN

			a, err := newApp(cfg)

			require.Error(t, err)
			assert.Nil(t, a)
			assert.True(t, strings.HasPrefix(err.Error(), tc.wantPrefix), "error = %q, want prefix %q", err, tc.wantPrefix)
			assert.Contains(t, err.Error(), tc.wantIn)
			assert.NotContains(t, err.Error(), "cmdsrv-secret-pw")
		})
	}
}

func TestNewApp_WiresTheSecretsStoreWhenOpenBaoIsConfigured(t *testing.T) {
	superDSN := cmdSrvSuperDSN(t)
	_, roleDSN := cmdSrvAppRole(t, superDSN)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "none")
	t.Setenv("LOG_FILE", filepath.Join(t.TempDir(), "app.log"))
	cfg := cmdSrvAppConfig(roleDSN)
	cfg.OpenBaoAddr = "http://127.0.0.1:1"
	cfg.OpenBaoToken = "cmdsrv-test-token"
	cfg.OpenBaoMount = "secret"
	cfg.TenantPurgeEnabled = true

	a, err := newApp(cfg)

	require.NoError(t, err)
	t.Cleanup(func() { cmdSrvCloseApp(t, a) })
	assert.NotNil(t, a.tenantPurger, "the purger is built with the secrets client")
	assert.NotNil(t, a.httpServer)
	assert.NotNil(t, a.metricsServer)
}
