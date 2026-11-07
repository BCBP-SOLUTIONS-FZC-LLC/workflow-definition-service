package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pgdomain "github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/config"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type cmdSrvLogEntry struct {
	level  string
	msg    string
	fields map[string]any
}

// cmdSrvRecorder is a port.Logger that keeps every call, safe for the
// goroutines the startup code launches.
type cmdSrvRecorder struct {
	mu      sync.Mutex
	entries []cmdSrvLogEntry
}

func (r *cmdSrvRecorder) add(level, msg string, fields map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, cmdSrvLogEntry{level: level, msg: msg, fields: fields})
}

func (r *cmdSrvRecorder) Debug(msg string, f map[string]any) { r.add("debug", msg, f) }
func (r *cmdSrvRecorder) Info(msg string, f map[string]any)  { r.add("info", msg, f) }
func (r *cmdSrvRecorder) Warn(msg string, f map[string]any)  { r.add("warn", msg, f) }
func (r *cmdSrvRecorder) Error(msg string, f map[string]any) { r.add("error", msg, f) }

func (r *cmdSrvRecorder) snapshot() []cmdSrvLogEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]cmdSrvLogEntry(nil), r.entries...)
}

func (r *cmdSrvRecorder) find(level, msg string) (cmdSrvLogEntry, bool) {
	for _, e := range r.snapshot() {
		if e.level == level && e.msg == msg {
			return e, true
		}
	}
	return cmdSrvLogEntry{}, false
}

var _ port.Logger = (*cmdSrvRecorder)(nil)

func TestPGCommonLogger_ForwardsEachLevelWithItsFields(t *testing.T) {
	cases := []struct {
		name       string
		call       func(l pgdomain.Logger, msg string, f ...pgdomain.Field)
		wantLevel  string
		msg        string
		fields     []pgdomain.Field
		wantFields map[string]any
	}{
		{
			name:       "debug",
			call:       pgdomain.Logger.Debug,
			wantLevel:  "debug",
			msg:        "acquired a connection",
			fields:     []pgdomain.Field{{Key: "pool", Value: "default"}, {Key: "waited_ms", Value: 3}},
			wantFields: map[string]any{"pool": "default", "waited_ms": 3},
		},
		{
			name:       "info",
			call:       pgdomain.Logger.Info,
			wantLevel:  "info",
			msg:        "pool ready",
			fields:     []pgdomain.Field{{Key: "max_conns", Value: int32(10)}},
			wantFields: map[string]any{"max_conns": int32(10)},
		},
		{
			name:       "warn",
			call:       pgdomain.Logger.Warn,
			wantLevel:  "warn",
			msg:        "slow query",
			fields:     []pgdomain.Field{{Key: "elapsed", Value: "450ms"}},
			wantFields: map[string]any{"elapsed": "450ms"},
		},
		{
			name:       "error",
			call:       pgdomain.Logger.Error,
			wantLevel:  "error",
			msg:        "ping failed",
			fields:     []pgdomain.Field{{Key: "error", Value: "connection refused"}},
			wantFields: map[string]any{"error": "connection refused"},
		},
		{
			name:       "a repeated key keeps the last value",
			call:       pgdomain.Logger.Info,
			wantLevel:  "info",
			msg:        "repeat",
			fields:     []pgdomain.Field{{Key: "attempt", Value: 1}, {Key: "other", Value: "x"}, {Key: "attempt", Value: 2}},
			wantFields: map[string]any{"attempt": 2, "other": "x"},
		},
		{
			name:       "no fields gives an empty map",
			call:       pgdomain.Logger.Warn,
			wantLevel:  "warn",
			msg:        "bare",
			wantFields: map[string]any{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &cmdSrvRecorder{}

			tc.call(newPGCommonLogger(rec), tc.msg, tc.fields...)

			entries := rec.snapshot()
			require.Len(t, entries, 1)
			assert.Equal(t, tc.wantLevel, entries[0].level)
			assert.Equal(t, tc.msg, entries[0].msg)
			assert.Equal(t, tc.wantFields, entries[0].fields)
		})
	}
}

func TestNewDBPool_ConnectFailureMessages(t *testing.T) {
	const (
		primaryDSN  = "postgres://primary_user:cmdsrv-secret-pw@127.0.0.1:1/wf?sslmode=disable"
		fallbackDSN = "postgres://fallback_user:cmdsrv-secret-pw@127.0.0.1:1/wf?sslmode=disable"
	)
	cases := []struct {
		name        string
		hasFallback bool
		wantPrefix  string
		wantContain string
		wantAbsent  string
	}{
		{
			name:        "no fallback configured reports only the primary failure",
			hasFallback: false,
			wantPrefix:  "db pool: ",
			wantContain: "primary_user",
			wantAbsent:  "fallback_user",
		},
		{
			name:        "primary and fallback both failing is reported as such",
			hasFallback: true,
			wantPrefix:  "db pool (primary and fallback both failed): ",
			wantContain: "fallback_user",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pools := poolConfigs{
				app:      pgcommon.Config{DSN: primaryDSN},
				fallback: pgcommon.Config{DSN: fallbackDSN},
			}

			pool, err := newDBPool(context.Background(), pools, tc.hasFallback)

			require.Error(t, err)
			assert.Nil(t, pool)
			assert.True(t, strings.HasPrefix(err.Error(), tc.wantPrefix), "error = %q, want prefix %q", err, tc.wantPrefix)
			assert.Contains(t, err.Error(), tc.wantContain)
			if tc.wantAbsent != "" {
				assert.NotContains(t, err.Error(), tc.wantAbsent)
			}
			assert.NotContains(t, err.Error(), "cmdsrv-secret-pw", "a connection password must never reach the init error")
		})
	}
}

func TestNewApp_DatabaseUnreachable_ReturnsInitError(t *testing.T) {
	cases := []struct {
		name       string
		fallback   string
		wantPrefix string
	}{
		{"no fallback", "", "db pool: "},
		{"with a fallback", "postgres://u:cmdsrv-secret-pw@127.0.0.1:1/wf?sslmode=disable", "db pool (primary and fallback both failed): "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "none")
			t.Setenv("LOG_FILE", filepath.Join(t.TempDir(), "app.log"))
			cfg := &config.Config{
				AppEnv:              "test",
				OTELServiceName:     "workflow-definition-service",
				DatabaseURL:         "postgres://u:cmdsrv-secret-pw@127.0.0.1:1/wf?sslmode=disable",
				DatabaseFallbackURL: tc.fallback,
			}

			a, err := newApp(cfg)

			require.Error(t, err)
			assert.Nil(t, a)
			assert.True(t, strings.HasPrefix(err.Error(), tc.wantPrefix), "error = %q, want prefix %q", err, tc.wantPrefix)
			assert.NotContains(t, err.Error(), "cmdsrv-secret-pw")
		})
	}
}

func TestNewApp_LoggerCannotBeBuilt_StopsBeforeAnyDatabaseWork(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o600))
	t.Setenv("LOG_FILE", filepath.Join(blocker, "app.log"))
	cfg := &config.Config{
		AppEnv:          "test",
		OTELServiceName: "workflow-definition-service",
		DatabaseURL:     "postgres://u:p@127.0.0.1:1/wf?sslmode=disable",
	}

	a, err := newApp(cfg)

	require.Error(t, err)
	assert.Nil(t, a)
	assert.True(t, strings.HasPrefix(err.Error(), "logger: "), "error = %q, want the logger failure and not the database one", err)
}

type cmdSrvPurgeRetention struct {
	port.TenantRetentionRepository
	listed chan struct{}
}

func (r *cmdSrvPurgeRetention) ListDue(context.Context, time.Time, int) ([]port.TenantPurgeDue, error) {
	select {
	case r.listed <- struct{}{}:
	default:
	}
	return nil, nil
}

type cmdSrvViolationLog struct {
	port.RLSViolationLog
	started chan struct{}
}

func (l *cmdSrvViolationLog) LatestID(context.Context) (int64, error) {
	select {
	case l.started <- struct{}{}:
	default:
	}
	return 0, nil
}

func (l *cmdSrvViolationLog) CountsAfter(context.Context, int64) ([]domain.RLSViolationCount, int64, error) {
	return nil, 0, nil
}

func (l *cmdSrvViolationLog) PruneBefore(context.Context, time.Time) (int64, error) { return 0, nil }

func cmdSrvLoopbackServer() *http.Server {
	return &http.Server{Addr: "127.0.0.1:0", Handler: http.NotFoundHandler(), ReadHeaderTimeout: time.Second}
}

func TestApp_StartServers_RunsThePurgerOnlyWhenOneIsWired(t *testing.T) {
	cases := []struct {
		name      string
		withPurge bool
	}{
		{"a wired purger is started next to the exporter", true},
		{"no purger means only the exporter runs", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &cmdSrvRecorder{}
			violations := &cmdSrvViolationLog{started: make(chan struct{}, 1)}
			retention := &cmdSrvPurgeRetention{listed: make(chan struct{}, 1)}
			a := &app{
				cfg:           &config.Config{},
				log:           rec,
				httpServer:    cmdSrvLoopbackServer(),
				metricsServer: cmdSrvLoopbackServer(),
				rlsExporter: service.NewRLSViolationExporter(service.RLSViolationExporterDeps{
					Log: violations, Record: func(string, float64) {}, Interval: time.Hour, Retention: time.Hour, Logger: rec,
				}),
			}
			if tc.withPurge {
				a.tenantPurger = service.NewTenantPurger(service.TenantPurgerDeps{
					Retention: retention, Interval: time.Hour, Batch: 1, Logger: rec,
					Record: func(string, int64) {}, Failed: func() {},
				})
			}
			fatal := make(chan error, 2)

			a.startServers(fatal)
			t.Cleanup(func() {
				a.cancelExport()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = a.httpServer.Shutdown(ctx)
				_ = a.metricsServer.Shutdown(ctx)
			})

			waitCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			select {
			case <-violations.started:
			case <-waitCtx.Done():
				t.Fatal("the violation exporter was never started")
			}
			if tc.withPurge {
				select {
				case <-retention.listed:
				case <-waitCtx.Done():
					t.Fatal("the wired tenant purger was never started")
				}
			}
			assert.Eventually(t, func() bool {
				_, httpUp := rec.find("info", "http server starting")
				_, metricsUp := rec.find("info", "metrics server starting")
				return httpUp && metricsUp
			}, 10*time.Second, 10*time.Millisecond, "both servers must be launched")
			if !tc.withPurge {
				assert.Never(t, func() bool {
					_, panicked := rec.find("error", "goroutine panicked, restarting")
					return panicked
				}, 300*time.Millisecond, 10*time.Millisecond, "starting without a purger must not touch a nil purger")
			}
		})
	}
}

func TestRunMigrations_Failure_IsReturnedAndNotReportedAsApplied(t *testing.T) {
	rec := &cmdSrvRecorder{}

	err := runMigrations(context.Background(), "postgres://u:cmdsrv-secret-pw@127.0.0.1:1/wf?sslmode=disable", rec)

	require.Error(t, err)
	assert.True(t, strings.HasPrefix(err.Error(), "domain migrations: "), "error = %q", err)
	assert.NotContains(t, err.Error(), "cmdsrv-secret-pw")
	_, started := rec.find("info", "running migrations")
	assert.True(t, started)
	_, applied := rec.find("info", "migrations applied")
	assert.False(t, applied, "a failed migration must not log that it applied")
}

// cmdSrvMainHelperEnv marks the child process that runs main().
const cmdSrvMainHelperEnv = "CMDSRV_MAIN_HELPER_ARGS"

// TestCmdSrvMainHelper is not a test of its own: the tests below run the test
// binary again with it selected, so that main and its os.Exit calls can be
// observed from outside. Run normally it does nothing.
func TestCmdSrvMainHelper(t *testing.T) {
	args, ok := os.LookupEnv(cmdSrvMainHelperEnv)
	if !ok {
		return
	}
	os.Args = append([]string{"server"}, strings.Fields(args)...)
	main()
}

type cmdSrvMainResult struct {
	exitCode int
	stdout   string
	stderr   string
}

// cmdSrvRunMain runs main() with args and the given environment on top of the
// parent one (minus its logging settings), in an empty working directory so no .env file is picked up.
func cmdSrvRunMain(t *testing.T, args string, env map[string]string) cmdSrvMainResult {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestCmdSrvMainHelper$")
	cmd.Dir = t.TempDir()
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "LOG_FILE=") || strings.HasPrefix(kv, "LOG_LEVEL=") ||
			strings.HasPrefix(kv, "LOG_SAMPLING=") {
			continue
		}
		cmd.Env = append(cmd.Env, kv)
	}
	cmd.Env = append(cmd.Env, "LOG_LEVEL=info", cmdSrvMainHelperEnv+"="+args)
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	err = cmd.Run()

	res := cmdSrvMainResult{stdout: stdout.String(), stderr: stderr.String()}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		res.exitCode = exitErr.ExitCode()
	default:
		t.Fatalf("run the test binary: %v", err)
	}
	return res
}

func TestMain_MigrateMode_LoggerFailureExitsWithLoggerError(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o600))

	res := cmdSrvRunMain(t, "migrate", map[string]string{
		"APP_ENV":      "dev",
		"DATABASE_URL": "postgres://u:cmdsrv-secret-pw@127.0.0.1:1/wf?sslmode=disable",
		"LOG_FILE":     filepath.Join(blocker, "app.log"),
	})

	assert.Equal(t, 1, res.exitCode)
	assert.Contains(t, res.stderr, "logger error: ")
	assert.NotContains(t, res.stderr, "migrate error", "the logger must fail before any migration is attempted")
	assert.NotContains(t, res.stderr, "init error")
}

func TestMain_MigrateMode_DatabaseFailureExitsWithMigrateError(t *testing.T) {
	res := cmdSrvRunMain(t, "migrate", map[string]string{
		"APP_ENV":      "dev",
		"DATABASE_URL": "postgres://u:cmdsrv-secret-pw@127.0.0.1:1/wf?sslmode=disable",
		"LOG_FILE":     filepath.Join(t.TempDir(), "app.log"),
	})

	assert.Equal(t, 1, res.exitCode)
	assert.Contains(t, res.stderr, "migrate error: domain migrations: ")
	assert.NotContains(t, res.stderr, "cmdsrv-secret-pw")
}

func TestMain_ServerMode_LoggerFailureExitsWithInitError(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o600))

	res := cmdSrvRunMain(t, "", map[string]string{
		"APP_ENV":      "dev",
		"DATABASE_URL": "postgres://u:p@127.0.0.1:1/wf?sslmode=disable",
		"LOG_FILE":     filepath.Join(blocker, "app.log"),
	})

	assert.Equal(t, 1, res.exitCode)
	assert.Contains(t, res.stderr, "init error: logger: ")
}

func TestMain_ServerMode_DatabaseDownExitsWithInitError(t *testing.T) {
	id := uuid.NewString()

	res := cmdSrvRunMain(t, "", map[string]string{
		"APP_ENV":                     "dev",
		"DATABASE_URL":                "postgres://u:" + id + "@127.0.0.1:1/wf?sslmode=disable",
		"LOG_FILE":                    filepath.Join(t.TempDir(), "app.log"),
		"OTEL_EXPORTER_OTLP_ENDPOINT": "none",
	})

	assert.Equal(t, 1, res.exitCode)
	assert.Contains(t, res.stderr, "init error: db pool: ")
	assert.NotContains(t, res.stderr, id, "a connection password must never reach stderr")
}
