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

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/config"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
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

		got, err := newPlanLimits(cfg, rec)

		require.NoError(t, err)
		assert.True(t, got == nil, "got %#v, want an untyped nil", got)
	})

	t.Run("a configured address builds the limit service", func(t *testing.T) {
		cfg := &config.Config{CatalogAdminBaseURL: "http://catalog:8080", CatalogClientTimeout: time.Second, PlanTableTTL: time.Minute}

		got, err := newPlanLimits(cfg, rec)

		require.NoError(t, err)
		require.NotNil(t, got)
	})
}

func TestNewPlanLimits_EntitlementsWinsOverTheCatalogWhenConfigured(t *testing.T) {
	rec := &cmdSrvRecorder{}
	base := config.Config{
		CatalogClientTimeout: time.Second, PlanTableTTL: time.Minute,
		EntitlementsClientTimeout: time.Second, EntitlementsCacheTTL: time.Minute, EntitlementsFreshnessBound: 10 * time.Minute,
	}

	t.Run("an entitlements address alone builds the entitlements service", func(t *testing.T) {
		cfg := base
		cfg.EntitlementsBaseURL = "http://entitlements:8080"

		got, err := newPlanLimits(&cfg, rec)

		require.NoError(t, err)
		assert.IsType(t, &service.EntitlementsLimitService{}, got)
	})

	t.Run("both addresses choose entitlements", func(t *testing.T) {
		cfg := base
		cfg.CatalogAdminBaseURL = "http://catalog:8080"
		cfg.EntitlementsBaseURL = "http://entitlements:8080"

		got, err := newPlanLimits(&cfg, rec)

		require.NoError(t, err)
		assert.IsType(t, &service.EntitlementsLimitService{}, got)
	})

	t.Run("only a catalog address keeps the plan table service", func(t *testing.T) {
		cfg := base
		cfg.CatalogAdminBaseURL = "http://catalog:8080"

		got, err := newPlanLimits(&cfg, rec)

		require.NoError(t, err)
		assert.IsType(t, &service.PlanLimitService{}, got)
	})

	t.Run("no address gives an untyped nil", func(t *testing.T) {
		cfg := base

		got, err := newPlanLimits(&cfg, rec)

		require.NoError(t, err)
		assert.True(t, got == nil, "got %#v, want an untyped nil", got)
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

func TestNewPlanLimits_LogsTheChosenSourceOnceAtStartup(t *testing.T) {
	base := config.Config{
		CatalogClientTimeout: time.Second, PlanTableTTL: time.Minute,
		EntitlementsClientTimeout: time.Second, EntitlementsCacheTTL: time.Minute, EntitlementsFreshnessBound: 10 * time.Minute,
	}
	cases := []struct {
		name        string
		entitlement string
		catalog     string
		wantLevel   string
		wantMsg     string
		wantSource  string
		wantIgnored bool
	}{
		{"entitlements alone", "http://entitlements:8080", "", "info", "plan limit source chosen", "entitlements", false},
		{"both addresses: entitlements wins and the catalog is named as ignored", "http://entitlements:8080", "http://catalog:8080", "info", "plan limit source chosen", "entitlements", true},
		{"catalog alone", "", "http://catalog:8080", "info", "plan limit source chosen", "plan_table", false},
		{"neither: creating, cloning and promoting will answer 503", "", "", "warn", "no plan limit source configured, creating, cloning and promoting workflows will answer 503", "none", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &cmdSrvRecorder{}
			cfg := base
			cfg.EntitlementsBaseURL = tc.entitlement
			cfg.CatalogAdminBaseURL = tc.catalog

			_, err := newPlanLimits(&cfg, rec)
			require.NoError(t, err)

			var lines []cmdSrvLogEntry
			for _, e := range rec.snapshot() {
				if e.msg == tc.wantMsg {
					lines = append(lines, e)
				}
			}
			require.Len(t, lines, 1, "exactly one startup line, got %v", rec.snapshot())
			assert.Equal(t, tc.wantLevel, lines[0].level)
			assert.Equal(t, tc.wantSource, lines[0].fields["source"])
			if tc.wantIgnored {
				assert.Equal(t, true, lines[0].fields["plan_table_ignored"])
			} else {
				assert.NotContains(t, lines[0].fields, "plan_table_ignored")
			}
		})
	}
}

func entitlementsConfig(url string, timeout, ttl, bound time.Duration) *config.Config {
	return &config.Config{
		EntitlementsBaseURL: url, EntitlementsClientTimeout: timeout,
		EntitlementsCacheTTL: ttl, EntitlementsFreshnessBound: bound,
	}
}

func TestNewPlanLimits_ABadEntitlementsSettingStopsStartupInsteadOfBuildingAHalfWorkingCache(t *testing.T) {
	for name, cfg := range map[string]*config.Config{
		"a zero TTL":            entitlementsConfig("http://entitlements", time.Second, 0, time.Minute),
		"a negative TTL":        entitlementsConfig("http://entitlements", time.Second, -time.Second, time.Minute),
		"a bound below the TTL": entitlementsConfig("http://entitlements", time.Second, time.Minute, time.Second),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := newPlanLimits(cfg, &cmdSrvRecorder{})

			require.Error(t, err)
			assert.ErrorContains(t, err, "entitlements")
			assert.True(t, got == nil, "got %#v, want an untyped nil", got)
		})
	}
}

// entitlementsAnswer serves the tenant's limit of 7, then 500 once failing is set, and counts its calls.
func entitlementsAnswer(t *testing.T, tenant uuid.UUID, failing *atomic.Bool, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		if failing.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"tenant_id":"` + tenant.String() + `","limits":{"workflow_template_limit":{"value":7}}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// A short TTL with a long bound pins both settings: once the TTL has passed and the server fails, the held limit
// still answers. Swapping the two, dropping the bound or using the catalog timeout fails this test.
func TestNewPlanLimits_AFailingRefreshAfterTheTTLKeepsServingTheHeldLimit(t *testing.T) {
	tenant := uuid.New()
	var failing atomic.Bool
	var calls atomic.Int32
	srv := entitlementsAnswer(t, tenant, &failing, &calls)
	rec := &cmdSrvRecorder{}
	limits, err := newPlanLimits(entitlementsConfig(srv.URL, time.Second, time.Nanosecond, time.Hour), rec)
	require.NoError(t, err)
	got, err := limits.WorkflowTemplateLimit(context.Background(), tenant, "")
	require.NoError(t, err)
	require.Equal(t, int64(7), *got)
	failing.Store(true)

	deadline := time.Now().Add(5 * time.Second)
	for len(entriesAt(rec, "warn", "entitlements refresh failed, serving the held limit")) == 0 {
		require.True(t, time.Now().Before(deadline), "the TTL never expired, so no refresh ran and failed")
		got, err = limits.WorkflowTemplateLimit(context.Background(), tenant, "")
		require.NoError(t, err, "the held limit is inside the bound")
		require.NotNil(t, got)
		assert.Equal(t, int64(7), *got)
	}

	assert.GreaterOrEqual(t, calls.Load(), int32(2))
}

func entriesAt(rec *cmdSrvRecorder, level, msg string) []cmdSrvLogEntry {
	var out []cmdSrvLogEntry
	for _, e := range rec.snapshot() {
		if e.level == level && e.msg == msg {
			out = append(out, e)
		}
	}
	return out
}

func TestNewPlanLimits_AFreshAnswerIsCachedForTheTTL(t *testing.T) {
	tenant := uuid.New()
	var failing atomic.Bool
	var calls atomic.Int32
	srv := entitlementsAnswer(t, tenant, &failing, &calls)
	limits, err := newPlanLimits(entitlementsConfig(srv.URL, time.Second, time.Hour, 2*time.Hour), &cmdSrvRecorder{})
	require.NoError(t, err)

	for range 3 {
		got, err := limits.WorkflowTemplateLimit(context.Background(), tenant, "")
		require.NoError(t, err)
		assert.Equal(t, int64(7), *got)
	}

	assert.EqualValues(t, 1, calls.Load())
}

func TestNewPlanLimits_ASlowEntitlementsServiceFailsAtTheEntitlementsTimeoutNotTheCatalogOne(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	t.Cleanup(func() { close(release); srv.Close() })
	cfg := entitlementsConfig(srv.URL, 50*time.Millisecond, time.Minute, 10*time.Minute)
	cfg.CatalogClientTimeout, cfg.PlanTableTTL = time.Hour, time.Minute
	limits, err := newPlanLimits(cfg, &cmdSrvRecorder{})
	require.NoError(t, err)

	start := time.Now()
	_, err = limits.WorkflowTemplateLimit(context.Background(), uuid.New(), "")

	require.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
	assert.Less(t, time.Since(start), 5*time.Second)
}
