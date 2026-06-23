package main

import (
	"context"
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/pkg/outbox"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/migrate"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/db/migrations"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

// serviceMigrationsTable keeps the service's domain migration history in a
// dedicated tracking table so it never collides with the outbox schema's
// migration history (which ApplySchema tracks under the pgcommon default table).
const serviceMigrationsTable = "wf_definition_migrations"

// runMigrations applies the outbox schema first (its tables are referenced by
// the service's outbox RLS migration), then the service's own domain migrations.
// It must complete before the connection pool is opened and the app starts.
//
// The migrate.Runner.Logger field is intentionally left unset: it requires
// platform-pgcommon's internal port.Logger (unexported Field type) which the
// service's port.Logger cannot satisfy (see CLAUDE.md §27). We log start/finish
// here instead.
func runMigrations(ctx context.Context, dsn string, log port.Logger) error {
	log.Info("running migrations", nil)

	// 1. Outbox tables (platform-events owns the DDL; tracked under its own table).
	if err := outbox.ApplySchema(ctx, &migrate.Runner{DSN: dsn}); err != nil {
		return fmt.Errorf("outbox schema: %w", err)
	}

	// 2. Service domain migrations (embedded FS, dedicated tracking table).
	domain := &migrate.Runner{
		FS:              migrations.FS,
		DSN:             dsn,
		MigrationsTable: serviceMigrationsTable,
	}
	if err := domain.Up(ctx); err != nil {
		return fmt.Errorf("domain migrations: %w", err)
	}

	log.Info("migrations applied", nil)
	return nil
}
