// Package fixtures provides shared helpers for integration tests: a
// containerised Postgres instance with migrations applied.
package fixtures

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgcommon"
)

// NewTestPool starts a throwaway Postgres 16 container, applies all goose
// migrations, and returns a *pgcommon.Pool connected to it.
//
// The test is skipped when testing.Short() is true so that unit tests can
// run without Docker. Cleanup (container termination) is registered via
// t.Cleanup.
func NewTestPool(t *testing.T) *pgcommon.Pool {
	t.Helper()

	if testing.Short() {
		t.Skip("skipping integration test: -short flag set (Docker required)")
	}

	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image:        "postgres:16-alpine",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_USER":     "postgres",
			"POSTGRES_PASSWORD": "postgres",
			"POSTGRES_DB":       "testdb",
		},
		WaitingFor: wait.ForLog("database system is ready to accept connections").
			WithOccurrence(2).
			WithStartupTimeout(120 * time.Second),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("fixtures.NewTestPool: start container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("fixtures.NewTestPool: container host: %v", err)
	}
	port, err := container.MappedPort(ctx, "5432")
	if err != nil {
		t.Fatalf("fixtures.NewTestPool: mapped port: %v", err)
	}

	dsn := fmt.Sprintf("postgres://postgres:postgres@%s:%s/testdb?sslmode=disable", host, port.Port())

	applyMigrations(t, dsn)

	pool, err := pgcommon.NewPool(ctx, pgcommon.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("fixtures.NewTestPool: create pool: %v", err)
	}
	t.Cleanup(pool.Close)

	return pool
}

// migrationsDir returns the absolute path to db/migrations/ relative to this
// source file so that it works regardless of the caller's working directory.
func migrationsDir() string {
	_, file, _, _ := runtime.Caller(0)
	// file = .../test/fixtures/testcontainer.go
	// go up two levels to the module root, then into db/migrations
	root := filepath.Join(filepath.Dir(file), "..", "..")
	return filepath.Join(root, "db", "migrations")
}

func applyMigrations(t *testing.T, dsn string) {
	t.Helper()

	dir := migrationsDir()
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("fixtures: migrations dir not found at %s: %v", dir, err)
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("fixtures: open db for migrations: %v", err)
	}
	defer db.Close() //nolint:errcheck

	goose.SetBaseFS(nil)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("fixtures: set goose dialect: %v", err)
	}
	if err := goose.Up(db, dir); err != nil {
		t.Fatalf("fixtures: goose up: %v", err)
	}
}
