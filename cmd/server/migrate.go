package main

import (
	"context"
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/migrate"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/db/migrations"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

// serviceMigrationsTable keeps the service's migration history apart from
// pgcommon's default tracking table.
const serviceMigrationsTable = "wf_definition_migrations"

// runMigrations must complete before the connection pool is opened and the
// app starts.
func runMigrations(ctx context.Context, dsn string, log port.Logger) error {
	log.Info("running migrations", nil)

	if err := applyDomainMigrations(ctx, dsn); err != nil {
		return err
	}

	log.Info("migrations applied", nil)
	return nil
}

// applyDomainMigrations runs the service's own migrations. Runner.Logger is
// left unset — see dbPoolConfig in infra.go for why platform-pgcommon's
// logger interface can't be implemented externally.
func applyDomainMigrations(ctx context.Context, dsn string) error {
	runner := &migrate.Runner{
		FS:              migrations.FS,
		DSN:             dsn,
		MigrationsTable: serviceMigrationsTable,
	}
	if err := runner.Up(ctx); err != nil {
		return fmt.Errorf("domain migrations: %w", err)
	}
	return nil
}
