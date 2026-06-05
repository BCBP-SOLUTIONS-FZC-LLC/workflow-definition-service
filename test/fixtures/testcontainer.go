package fixtures

import (
	"context"
	"database/sql"
	"path/filepath"
	"runtime"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgcommon"
)

// NewTestPool starts a Postgres container, applies all goose migrations, and
// returns a pgcommon.Pool connected to it. The container and pool are
// automatically cleaned up when t ends.
func NewTestPool(t *testing.T) *pgcommon.Pool {
	t.Helper()
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(context.Background()) })

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open sql.DB: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	if err := goose.Up(sqlDB, MigrationsDir()); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	pool, err := pgcommon.NewPool(ctx, pgcommon.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("pgcommon pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// MigrationsDir resolves the absolute path to db/migrations/ relative to this
// file's location so it works regardless of where go test is invoked from.
func MigrationsDir() string {
	_, filename, _, _ := runtime.Caller(0)
	// filename: .../test/fixtures/testcontainer.go
	// walk up 2 levels to the project root
	root := filepath.Join(filepath.Dir(filename), "..", "..")
	return filepath.Join(root, "db", "migrations")
}
