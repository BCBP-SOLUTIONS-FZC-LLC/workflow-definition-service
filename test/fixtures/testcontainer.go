// Package fixtures provides shared helpers for integration tests: a
// containerised Postgres instance with migrations applied.
package fixtures

import (
	"context"
	"fmt"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/pkg/outbox"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/migrate"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/db/migrations"
)

// serviceMigrationsTable mirrors cmd/server's tracking table so the test schema
// matches production: outbox migrations and domain migrations are tracked apart.
const serviceMigrationsTable = "wf_definition_migrations"

// NewTestPool starts a throwaway Postgres 18 container, applies the outbox and
// domain migrations, and returns a *pgcommon.Pool connected to it.
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

// applyMigrations runs the outbox schema first (its tables are referenced by the
// service's outbox RLS migration) then the embedded domain migrations, matching
// the production startup path in cmd/server/migrate.go.
func applyMigrations(t *testing.T, dsn string) {
	t.Helper()

	ctx := context.Background()
	if err := outbox.ApplySchema(ctx, &migrate.Runner{DSN: dsn}); err != nil {
		t.Fatalf("fixtures: outbox ApplySchema: %v", err)
	}
	domainRunner := &migrate.Runner{
		FS:              migrations.FS,
		DSN:             dsn,
		MigrationsTable: serviceMigrationsTable,
	}
	if err := domainRunner.Up(ctx); err != nil {
		t.Fatalf("fixtures: domain migrations: %v", err)
	}
}
