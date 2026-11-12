//go:build integration

package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
)

func TestApp_Run_AListenerFailureSkipsTheDrainDelayAndStillFlipsReadinessAndFlushesTracing(t *testing.T) {
	r := cmdSrvCovBuild(t)
	r.app.cfg.ShutdownDrainDelay = time.Hour
	held, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = held.Close() })
	r.app.metricsServer.Addr = held.Addr().String()

	guard := make(chan os.Signal, 1)
	signal.Notify(guard, syscall.SIGTERM)
	t.Cleanup(func() { signal.Stop(guard) })

	done := make(chan error, 1)
	go func() { done <- r.app.run() }()

	var runErr error
	select {
	case runErr = <-done:
	case <-time.After(cmdSrvCovWait):
		t.Fatal("run kept waiting for the one-hour drain delay after a listener failed")
	}

	require.Error(t, runErr)
	assert.True(t, strings.HasPrefix(runErr.Error(), "metrics server: "), "error = %q", runErr)
	assert.Contains(t, runErr.Error(), "address already in use")
	shutting := cmdSrvCovLogIndex(r.rec, "info", "shutting down")
	stopped := cmdSrvCovLogIndex(r.rec, "info", "server stopped")
	require.GreaterOrEqual(t, shutting, 0)
	assert.Less(t, shutting, stopped)
	cmdSrvCovAssertStopped(t, r)
}

func TestNewApp_TenantPurgerCountsTheFailingCredentialsPart(t *testing.T) {
	superDSN := cmdSrvSuperDSN(t)
	_, roleDSN := cmdSrvAppRole(t, superDSN)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "none")
	t.Setenv("LOG_FILE", filepath.Join(t.TempDir(), "app.log"))
	openbao := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "sealed", http.StatusInternalServerError)
	}))
	t.Cleanup(openbao.Close)
	cfg := cmdSrvAppConfig(roleDSN)
	cfg.OpenBaoAddr = openbao.URL
	cfg.OpenBaoToken = "cmdsrv-test-token"
	cfg.OpenBaoMount = "secret"
	cfg.TenantPurgeEnabled = true

	a, err := newApp(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { cmdSrvCloseApp(t, a) })

	ctx := context.Background()
	retention := pgadapter.NewTenantRetentionRepo(a.pool)
	now := time.Now()
	tenant := uuid.New()
	require.NoError(t, retention.RecordOffboarded(ctx, tenant, now.Add(-3*time.Hour), now.Add(-2*time.Hour), now.Add(24*time.Hour)))
	t.Cleanup(func() { require.NoError(t, retention.RecordReturned(context.Background(), tenant, time.Now())) })
	due, err := retention.ListDue(ctx, now, 10)
	require.NoError(t, err)
	require.Len(t, due, 1)
	require.True(t, due[0].Credentials)
	require.False(t, due[0].Artifacts)

	before := testutil.ToFloat64(observability.TenantPurgeFailuresTotal)
	tickErr := a.tenantPurger.Tick(ctx)

	require.Error(t, tickErr, "the secrets store refused the credential delete")
	assert.NotContains(t, tickErr.Error(), "cmdsrv-test-token")
	assert.InDelta(t, 1, testutil.ToFloat64(observability.TenantPurgeFailuresTotal)-before, 0,
		"the failing credentials part adds one to the purge failure counter")
}
