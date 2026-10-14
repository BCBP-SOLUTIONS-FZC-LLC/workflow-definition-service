package main

import (
	"context"
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/migrate"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/db/migrations"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

const serviceMigrationsTable = "wf_definition_migrations"

func runMigrations(ctx context.Context, dsn string, log port.Logger) error {
	log.Info("running migrations", nil)

	if err := applyDomainMigrations(ctx, dsn); err != nil {
		return err
	}

	log.Info("migrations applied", nil)
	return nil
}

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
