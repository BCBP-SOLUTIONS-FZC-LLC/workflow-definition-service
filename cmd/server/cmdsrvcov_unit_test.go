package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/config"
)

// cmdSrvCovCaptureStderr redirects os.Stderr into a pipe while fn runs and
// returns what was written. The original descriptor is restored before it
// returns, also when fn panics.
func cmdSrvCovCaptureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	orig := os.Stderr
	os.Stderr = w
	collected := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		collected <- string(b)
	}()
	func() {
		defer func() {
			os.Stderr = orig
			_ = w.Close()
		}()
		fn()
	}()
	out := <-collected
	_ = r.Close()
	return out
}

func cmdSrvCovPortOf(t *testing.T, serverURL string) string {
	t.Helper()
	u, err := url.Parse(serverURL)
	require.NoError(t, err)
	return u.Port()
}

func TestRunHealthcheck_ExitCodeFollowsTheHealthEndpoint(t *testing.T) {
	var hits atomic.Int32
	status := atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		hits.Add(1)
		w.WriteHeader(int(status.Load()))
	}))
	t.Cleanup(srv.Close)

	t.Run("a 200 from /healthz exits 0 and prints nothing", func(t *testing.T) {
		status.Store(http.StatusOK)
		t.Setenv("HTTP_PORT", cmdSrvCovPortOf(t, srv.URL))
		before := hits.Load()
		var code int

		stderr := cmdSrvCovCaptureStderr(t, func() { code = runHealthcheck() })

		assert.Equal(t, 0, code)
		assert.Empty(t, stderr)
		assert.Equal(t, before+1, hits.Load(), "exactly one probe of /healthz is made")
	})

	t.Run("a 503 exits 1 and names the status on stderr", func(t *testing.T) {
		status.Store(http.StatusServiceUnavailable)
		t.Setenv("HTTP_PORT", cmdSrvCovPortOf(t, srv.URL))
		var code int

		stderr := cmdSrvCovCaptureStderr(t, func() { code = runHealthcheck() })

		assert.Equal(t, 1, code)
		assert.True(t, strings.HasPrefix(stderr, "healthcheck: "), "stderr = %q", stderr)
		assert.Contains(t, stderr, "healthz returned 503")
	})

	t.Run("a refused connection exits 1 and says the request failed", func(t *testing.T) {
		t.Setenv("HTTP_PORT", "1")
		var code int

		stderr := cmdSrvCovCaptureStderr(t, func() { code = runHealthcheck() })

		assert.Equal(t, 1, code)
		assert.True(t, strings.HasPrefix(stderr, "healthcheck: get healthz: "), "stderr = %q", stderr)
		assert.True(t, strings.HasSuffix(stderr, "\n"))
	})
}

func TestProbeHealth_AnUnparsableBaseURLIsReportedBeforeAnyRequest(t *testing.T) {
	err := probeHealth(context.Background(), "http://bad host")

	require.Error(t, err)
	assert.True(t, strings.HasPrefix(err.Error(), "build healthz request: "), "error = %q", err)
}

func TestNewPlanLimits_FollowsTheCatalogAddress(t *testing.T) {
	rec := &cmdSrvRecorder{}

	t.Run("an unset address gives a nil interface so services skip plan limits", func(t *testing.T) {
		cfg := &config.Config{CatalogClientTimeout: time.Second, PlanTableTTL: time.Minute}

		got := newPlanLimits(cfg, rec)

		assert.True(t, got == nil, "got %#v, want an untyped nil", got)
	})

	t.Run("a configured address builds the limit service", func(t *testing.T) {
		cfg := &config.Config{CatalogAdminBaseURL: "http://catalog:8080", CatalogClientTimeout: time.Second, PlanTableTTL: time.Minute}

		got := newPlanLimits(cfg, rec)

		require.NotNil(t, got)
	})
}

func TestInitTelemetry_RejectsAMissingServiceName(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "none")
	rec := &cmdSrvRecorder{}

	shutdown, err := initTelemetry(gincommon.Config{Logger: rec, ServiceName: "  ", Environment: "test"}, rec)

	require.Error(t, err)
	assert.Nil(t, shutdown)
	assert.True(t, strings.HasPrefix(err.Error(), "tracing: "), "error = %q", err)
	assert.ErrorIs(t, err, gincommon.ErrServiceNameRequired)
}

func TestNewApp_MissingServiceNameStopsBeforeAnyDatabaseWork(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "none")
	t.Setenv("LOG_FILE", filepath.Join(t.TempDir(), "app.log"))
	cfg := &config.Config{
		AppEnv:      "test",
		DatabaseURL: "postgres://u:cmdsrv-secret-pw@127.0.0.1:1/wf?sslmode=disable",
	}

	a, err := newApp(cfg)

	require.Error(t, err)
	assert.Nil(t, a)
	assert.True(t, strings.HasPrefix(err.Error(), "tracing: "), "error = %q, want the telemetry failure and not the database one", err)
}
