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

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/migrate"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/db/migrations"
)

// serviceMigrationsTable mirrors cmd/server's tracking table so the test schema
// matches production.
const serviceMigrationsTable = "wf_definition_migrations"

// NewTestPool starts a throwaway Postgres 18 container, applies the domain
// migrations, and returns a *pgcommon.Pool connected to it.
//
// The test is skipped when testing.Short() is true so that unit tests can
// run without Docker. Cleanup (container termination) is registered via
// t.Cleanup.
func NewTestPool(t *testing.T) *pgcommon.Pool {
	pool, _ := NewTestPoolAndDSN(t)
	return pool
}

// NewTestPoolAndDSN is like NewTestPool but also returns the raw connection DSN.
// Use this when you need to create additional pools against the same container
// (e.g. tenant-scoped pools for RLS tests).
func NewTestPoolAndDSN(t *testing.T) (*pgcommon.Pool, string) {
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

	container, host, port, err := startWithMappedPort(ctx, req, func(ctx context.Context, c testcontainers.Container) (string, error) {
		p, err := c.MappedPort(ctx, "5432")
		return p.Port(), err
	})
	if err != nil {
		t.Fatalf("fixtures.NewTestPoolAndDSN: start container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	dsn := fmt.Sprintf("postgres://postgres:postgres@%s:%s/testdb?sslmode=disable", host, port)

	applyMigrations(t, dsn)

	pool, err := pgcommon.NewPool(ctx, pgcommon.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("fixtures.NewTestPoolAndDSN: create pool: %v", err)
	}
	t.Cleanup(pool.Close)

	return pool, dsn
}

// applyMigrations runs the embedded domain migrations, matching the
// production startup path in cmd/server/migrate.go.
func applyMigrations(t *testing.T, dsn string) {
	t.Helper()

	ctx := context.Background()
	domainRunner := &migrate.Runner{
		FS:              migrations.FS,
		DSN:             dsn,
		MigrationsTable: serviceMigrationsTable,
	}
	if err := domainRunner.Up(ctx); err != nil {
		t.Fatalf("fixtures: domain migrations: %v", err)
	}
}

const (
	startAttempts   = 3
	portMapTimeout  = 20 * time.Second
	portMapInterval = 200 * time.Millisecond
)

// startWithMappedPort starts a container and waits for Docker to publish one of its ports. A
// container whose port is never published (a Docker Desktop fault under load) is replaced rather
// than waited on, since the library's own lookup would retry for minutes.
func startWithMappedPort(
	ctx context.Context,
	req testcontainers.ContainerRequest,
	lookup func(context.Context, testcontainers.Container) (string, error),
) (testcontainers.Container, string, string, error) {
	var lastErr error
	for attempt := 0; attempt < startAttempts; attempt++ {
		container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: req,
			Started:          true,
		})
		if err != nil {
			lastErr = err
			if container != nil {
				_ = container.Terminate(ctx)
			}
			continue
		}
		host, err := container.Host(ctx)
		if err == nil {
			var port string
			if port, err = mappedPortWithin(ctx, container, lookup); err == nil {
				return container, host, port, nil
			}
		}
		lastErr = err
		_ = container.Terminate(ctx)
	}
	return nil, "", "", fmt.Errorf("a container with a published port after %d attempts: %w", startAttempts, lastErr)
}

func mappedPortWithin(ctx context.Context, container testcontainers.Container, lookup func(context.Context, testcontainers.Container) (string, error)) (string, error) {
	lookupCtx, cancel := context.WithTimeout(ctx, portMapTimeout)
	defer cancel()
	var lastErr error
	for lookupCtx.Err() == nil {
		port, err := lookup(lookupCtx, container)
		if err == nil {
			return port, nil
		}
		lastErr = err
		select {
		case <-lookupCtx.Done():
		case <-time.After(portMapInterval):
		}
	}
	return "", lastErr
}
