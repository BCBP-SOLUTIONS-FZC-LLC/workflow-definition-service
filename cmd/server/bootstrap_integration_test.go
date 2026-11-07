//go:build integration

package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/migrate"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/db/migrations"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/config"
)

// One Postgres container serves every test here that only needs a superuser
// connection: each test creates its own uniquely named databases and roles on
// it. It starts on first use, so a run that selects no such test never starts
// Docker, and TestMain terminates it once all tests have finished.
var (
	cmdSrvPGOnce      sync.Once
	cmdSrvPGContainer testcontainers.Container
	cmdSrvPGDSN       string
	cmdSrvPGErr       error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if cmdSrvPGContainer != nil {
		_ = cmdSrvPGContainer.Terminate(context.Background())
	}
	os.Exit(code)
}

func cmdSrvStartPostgres() (string, error) {
	ctx := context.Background()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "postgres:18-alpine",
			ExposedPorts: []string{"5432/tcp"},
			Env: map[string]string{
				"POSTGRES_USER":     "postgres",
				"POSTGRES_PASSWORD": "postgres",
				"POSTGRES_DB":       "testdb",
			},
			WaitingFor: wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(120 * time.Second),
		},
		Started: true,
	})
	if container != nil {
		cmdSrvPGContainer = container
	}
	if err != nil {
		return "", fmt.Errorf("start container: %w", err)
	}
	host, err := container.Host(ctx)
	if err != nil {
		return "", fmt.Errorf("container host: %w", err)
	}
	port, err := container.MappedPort(ctx, "5432/tcp")
	if err != nil {
		return "", fmt.Errorf("container port: %w", err)
	}
	dsn := fmt.Sprintf("postgres://postgres:postgres@%s:%s/testdb?sslmode=disable", host, port.Port())
	runner := &migrate.Runner{FS: migrations.FS, DSN: dsn, MigrationsTable: "wf_definition_migrations"}
	if err := runner.Up(ctx); err != nil {
		return "", fmt.Errorf("domain migrations: %w", err)
	}
	return dsn, nil
}

// cmdSrvSuperDSN returns the superuser DSN of the shared container's migrated
// testdb database, starting the container on first use.
func cmdSrvSuperDSN(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test: -short flag set (Docker required)")
	}
	cmdSrvPGOnce.Do(func() { cmdSrvPGDSN, cmdSrvPGErr = cmdSrvStartPostgres() })
	require.NoError(t, cmdSrvPGErr, "the shared Postgres container is unavailable")
	return cmdSrvPGDSN
}

func cmdSrvOpenAdmin(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// cmdSrvEmptyDatabase creates a database with no schema on the container behind
// superDSN and returns its DSN.
func cmdSrvEmptyDatabase(t *testing.T, superDSN string) string {
	t.Helper()
	name := "cmdsrv_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	require.Contains(t, superDSN, "/testdb?")
	_, err := cmdSrvOpenAdmin(t, superDSN).ExecContext(context.Background(), "CREATE DATABASE "+name)
	require.NoError(t, err)
	return strings.Replace(superDSN, "/testdb?", "/"+name+"?", 1)
}

func cmdSrvRegclass(t *testing.T, dsn, table string) bool {
	t.Helper()
	var found bool
	err := cmdSrvOpenAdmin(t, dsn).QueryRowContext(context.Background(),
		`SELECT to_regclass($1) IS NOT NULL`, "public."+table).Scan(&found)
	require.NoError(t, err)
	return found
}

// cmdSrvAppRole creates a login role that cannot bypass row-level security but
// can write the tables, and returns its name and DSN.
func cmdSrvAppRole(t *testing.T, superDSN string) (string, string) {
	t.Helper()
	role := "restricted_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	db := cmdSrvOpenAdmin(t, superDSN)
	for _, stmt := range []string{
		fmt.Sprintf(`CREATE ROLE %s LOGIN PASSWORD 'cmdsrv' NOBYPASSRLS`, role),
		fmt.Sprintf(`GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO %s`, role),
	} {
		_, err := db.ExecContext(context.Background(), stmt)
		require.NoError(t, err)
	}
	before, after, _ := strings.Cut(superDSN, "@")
	scheme, _, _ := strings.Cut(before, "://")
	return role, fmt.Sprintf("%s://%s:cmdsrv@%s", scheme, role, after)
}

func cmdSrvCloseApp(t *testing.T, a *app) {
	t.Helper()
	a.pool.Close()
	a.systemPool.Close()
	_ = a.cacheClose.Close()
	a.shutdown()
}

// cmdSrvReadLog returns the JSON lines the logger wrote to LOG_FILE.
func cmdSrvReadLog(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var line map[string]any
		if err := json.Unmarshal(sc.Bytes(), &line); err == nil {
			out = append(out, line)
		}
	}
	require.NoError(t, sc.Err())
	return out
}

func cmdSrvLogLines(lines []map[string]any, level, msg string) []map[string]any {
	var out []map[string]any
	for _, l := range lines {
		if l["level"] == level && l["msg"] == msg {
			out = append(out, l)
		}
	}
	return out
}

func cmdSrvAppConfig(dsn string) *config.Config {
	return &config.Config{
		AppEnv:                     "test",
		OTELServiceName:            "workflow-definition-service",
		DatabaseURL:                dsn,
		ValkeyAddr:                 "127.0.0.1:1",
		ValkeyDialTimeout:          200 * time.Millisecond,
		ValkeyReadTimeout:          200 * time.Millisecond,
		ValkeyWriteTimeout:         200 * time.Millisecond,
		CacheCompiledPlanTTL:       time.Hour,
		IdempotencyTTL:             time.Hour,
		RLSViolationExportInterval: time.Hour,
		RLSViolationRetention:      time.Hour,
		TenantCredentialRetention:  time.Hour,
		TenantArtifactRetention:    time.Hour,
		TenantPurgeInterval:        time.Hour,
		TenantPurgeBatch:           1,
		SecretsClientTimeout:       time.Second,
		MembershipClientTimeout:    time.Second,
		CatalogClientTimeout:       time.Second,
		ExecutionClientTimeout:     time.Second,
		PlanTableTTL:               time.Minute,
	}
}

func TestDBPinger_Health_ReportsThePoolUnchanged(t *testing.T) {
	dsn := cmdSrvSuperDSN(t)
	ctx := context.Background()
	pool, err := pgcommon.NewPool(ctx, pgcommon.Config{DSN: dsn, MaxConns: 7})
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	pinger := dbPinger{pool: pool}

	t.Run("an open pool is healthy and carries its sizes", func(t *testing.T) {
		err := pool.WithConn(ctx, func(ctx context.Context, _ *pgxpool.Conn) error {
			got := pinger.Health(ctx)

			assert.True(t, got.Healthy)
			assert.Equal(t, int32(7), got.MaxConns)
			assert.GreaterOrEqual(t, got.AcquiredConns, int32(1), "the connection held by this test must be counted")
			assert.InDelta(t, float64(got.AcquiredConns)/float64(got.MaxConns), got.Utilization, 1e-9)
			return nil
		})
		require.NoError(t, err)
	})

	t.Run("a closed pool is reported unhealthy", func(t *testing.T) {
		closed, err := pgcommon.NewPool(ctx, pgcommon.Config{DSN: dsn, MaxConns: 7})
		require.NoError(t, err)
		closed.Close()

		got := dbPinger{pool: closed}.Health(ctx)

		assert.False(t, got.Healthy)
		assert.Zero(t, got.AcquiredConns)
		assert.Zero(t, got.Utilization)
	})
}

func TestVerifyPoolRoles_LookupFailureIsReportedPerPool(t *testing.T) {
	dsn := cmdSrvSuperDSN(t)
	ctx := context.Background()
	open := func(t *testing.T, roleBypass bool) *pgcommon.Pool {
		t.Helper()
		return cmdSrvRolePool(t, dsn, roleBypass)
	}

	t.Run("healthy pools return both roles", func(t *testing.T) {
		appPool, systemPool := open(t, false), open(t, true)

		warnings, app, sys, err := verifyPoolRoles(ctx, appPool, systemPool, "prod", true)

		require.NoError(t, err)
		assert.Empty(t, warnings)
		assert.True(t, strings.HasPrefix(app.Role, "restricted_"))
		assert.False(t, app.BypassesRLS())
		assert.True(t, sys.BypassRLS)
		assert.NotEqual(t, app.Role, sys.Role)
	})

	t.Run("a failing application pool is named in the error", func(t *testing.T) {
		appPool, err := pgcommon.NewPool(ctx, pgcommon.Config{DSN: dsn})
		require.NoError(t, err)
		appPool.Close()

		warnings, _, _, err := verifyPoolRoles(ctx, appPool, open(t, true), "prod", true)

		require.Error(t, err)
		assert.True(t, strings.HasPrefix(err.Error(), "read the application pool's role: "), "error = %q", err)
		assert.Nil(t, warnings)
	})

	t.Run("a failing system pool is named in the error", func(t *testing.T) {
		systemPool, err := pgcommon.NewPool(ctx, pgcommon.Config{DSN: dsn})
		require.NoError(t, err)
		systemPool.Close()

		warnings, app, _, err := verifyPoolRoles(ctx, open(t, false), systemPool, "prod", true)

		require.Error(t, err)
		assert.True(t, strings.HasPrefix(err.Error(), "read the system pool's role: "), "error = %q", err)
		assert.Nil(t, warnings)
		assert.True(t, strings.HasPrefix(app.Role, "restricted_"), "the application role was read before the system lookup failed")
	})

	t.Run("an already expired context fails the first lookup", func(t *testing.T) {
		expired, cancel := context.WithCancel(ctx)
		cancel()

		_, _, _, err := verifyPoolRoles(expired, open(t, false), open(t, true), "prod", true)

		require.Error(t, err)
		assert.True(t, strings.HasPrefix(err.Error(), "read the application pool's role: "), "error = %q", err)
	})
}

func TestNewApp_RolePostureIsLogged(t *testing.T) {
	superDSN := cmdSrvSuperDSN(t)
	role, roleDSN := cmdSrvAppRole(t, superDSN)

	cases := []struct {
		name       string
		systemDSN  string
		wantWarn   bool
		wantSystem string
		wantBypass bool
	}{
		{"a system pool on the application role warns in dev", "", true, role, false},
		{"a BYPASSRLS system role logs no role warning", superDSN, false, "postgres", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logPath := filepath.Join(t.TempDir(), "app.log")
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "none")
			t.Setenv("LOG_FILE", logPath)
			t.Setenv("PG_MAX_CONNS", "not-a-number")
			t.Setenv("DATABASE_URL", roleDSN)
			cfg := cmdSrvAppConfig(roleDSN)
			cfg.SystemDatabaseURL = tc.systemDSN

			a, err := newApp(cfg)

			require.NoError(t, err)
			t.Cleanup(func() { cmdSrvCloseApp(t, a) })
			lines := cmdSrvReadLog(t, logPath)

			var maxConnsWarning map[string]any
			for _, l := range cmdSrvLogLines(lines, "warn", "postgres config warning") {
				if l["key"] == "PG_MAX_CONNS" {
					maxConnsWarning = l
				}
			}
			require.NotNil(t, maxConnsWarning, "an invalid PG_MAX_CONNS must be logged as a warning")
			assert.Contains(t, maxConnsWarning["reason"], "not-a-number")

			var roleWarnings []string
			for _, l := range lines {
				if msg, _ := l["msg"].(string); l["level"] == "warn" && strings.Contains(msg, "database role") {
					roleWarnings = append(roleWarnings, msg)
				}
			}
			if tc.wantWarn {
				require.NotEmpty(t, roleWarnings)
				assert.Contains(t, roleWarnings[0], "cannot bypass row-level security")
				assert.Contains(t, roleWarnings[0], role)
			} else {
				assert.Empty(t, roleWarnings)
			}

			verified := cmdSrvLogLines(lines, "info", "database role posture verified")
			require.Len(t, verified, 1)
			assert.Equal(t, role, verified[0]["app_role"])
			assert.Equal(t, tc.wantSystem, verified[0]["system_role"])
			assert.Equal(t, tc.wantBypass, verified[0]["system_bypassrls"])
		})
	}
}

func TestNewApp_TenantPurgerFollowsConfig(t *testing.T) {
	superDSN := cmdSrvSuperDSN(t)
	_, roleDSN := cmdSrvAppRole(t, superDSN)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "none")

	cases := []struct {
		name    string
		enabled bool
	}{
		{"enabled builds a purger", true},
		{"disabled builds no purger", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LOG_FILE", filepath.Join(t.TempDir(), "app.log"))
			cfg := cmdSrvAppConfig(roleDSN)
			cfg.TenantPurgeEnabled = tc.enabled

			a, err := newApp(cfg)

			require.NoError(t, err)
			t.Cleanup(func() { cmdSrvCloseApp(t, a) })
			if !tc.enabled {
				assert.Nil(t, a.tenantPurger)
				return
			}
			assert.NotNil(t, a.tenantPurger)
		})
	}
}

func TestApplyDomainMigrations_EmptyDatabaseThenRerun(t *testing.T) {
	superDSN := cmdSrvSuperDSN(t)
	dsn := cmdSrvEmptyDatabase(t, superDSN)
	ctx := context.Background()
	require.False(t, cmdSrvRegclass(t, dsn, "wf_definition_migrations"))

	require.NoError(t, applyDomainMigrations(ctx, dsn))

	assert.True(t, cmdSrvRegclass(t, dsn, "wf_definition_migrations"), "the service's own migration table must be used")
	assert.True(t, cmdSrvRegclass(t, dsn, "workflow_template"), "the domain schema must exist")
	count := func() (int, int) {
		var version, n int
		require.NoError(t, cmdSrvOpenAdmin(t, dsn).QueryRowContext(ctx,
			`SELECT max(version), count(*) FROM wf_definition_migrations`).Scan(&version, &n))
		return version, n
	}
	version, n := count()
	assert.Positive(t, version)

	require.NoError(t, applyDomainMigrations(ctx, dsn), "running again is a no-op")
	version2, n2 := count()
	assert.Equal(t, version, version2)
	assert.Equal(t, n, n2)
}

func TestRunMigrations_Success_LogsStartAndFinish(t *testing.T) {
	superDSN := cmdSrvSuperDSN(t)
	rec := &cmdSrvRecorder{}

	err := runMigrations(context.Background(), cmdSrvEmptyDatabase(t, superDSN), rec)

	require.NoError(t, err)
	_, started := rec.find("info", "running migrations")
	_, applied := rec.find("info", "migrations applied")
	assert.True(t, started)
	assert.True(t, applied)
}

func TestMain_MigrateMode_ChoosesTheDatabaseAndExitsCleanly(t *testing.T) {
	superDSN := cmdSrvSuperDSN(t)
	const unreachable = "postgres://u:cmdsrv-secret-pw@127.0.0.1:1/wf?sslmode=disable"

	cases := []struct {
		name string
		env  func(target string) map[string]string
	}{
		{"MIGRATION_DATABASE_URL wins over DATABASE_URL", func(target string) map[string]string {
			return map[string]string{"DATABASE_URL": unreachable, "MIGRATION_DATABASE_URL": target}
		}},
		{"DATABASE_URL is used when no migration URL is set", func(target string) map[string]string {
			return map[string]string{"DATABASE_URL": target}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := cmdSrvEmptyDatabase(t, superDSN)
			env := tc.env(target)
			env["APP_ENV"] = "dev"
			env["LOG_FILE"] = filepath.Join(t.TempDir(), "app.log")

			res := cmdSrvRunMain(t, "migrate", env)

			assert.Equal(t, 0, res.exitCode, "stderr: %s", res.stderr)
			assert.NotContains(t, res.stderr, "migrate error")
			assert.NotContains(t, res.stderr, "init error")
			assert.True(t, cmdSrvRegclass(t, target, "wf_definition_migrations"))
			assert.True(t, cmdSrvRegclass(t, target, "workflow_template"))
		})
	}
}

func cmdSrvRolePool(t *testing.T, superDSN string, bypassRLS bool) *pgcommon.Pool {
	t.Helper()
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, superDSN)
	require.NoError(t, err)
	t.Cleanup(admin.Close)

	role := "restricted_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	clause := "NOBYPASSRLS"
	if bypassRLS {
		clause = "BYPASSRLS"
	}
	_, err = admin.Exec(ctx, fmt.Sprintf(`CREATE ROLE %s LOGIN PASSWORD 'restricted' %s`, role, clause))
	require.NoError(t, err)
	_, err = admin.Exec(ctx, fmt.Sprintf(`GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO %s`, role))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), fmt.Sprintf(`DROP OWNED BY %s`, role))
		_, _ = admin.Exec(context.Background(), fmt.Sprintf(`DROP ROLE IF EXISTS %s`, role))
	})

	before, after, _ := strings.Cut(superDSN, "@")
	scheme, _, _ := strings.Cut(before, "://")
	pool, err := pgcommon.NewPool(ctx, pgcommon.Config{DSN: fmt.Sprintf("%s://%s:restricted@%s", scheme, role, after)})
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}
