package postgres

import (
	"context"
	"errors"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgcommon"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres/db"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

// exec runs fn with either the transaction stored in ctx (when inside a
// Transactor.RunInTx callback) or a fresh connection from pool.
func exec(ctx context.Context, pool *pgcommon.Pool, fn func(db.DBTX) error) error {
	if tx, ok := txFromContext(ctx); ok {
		return fn(tx)
	}
	return pool.WithConn(ctx, func(_ context.Context, conn *pgxpool.Conn) error { //nolint:wrapcheck
		return fn(conn)
	})
}

// statusOrNotFound is called when a status-guarded UPDATE affects 0 rows.
// It probes for the version's existence: if absent → ErrNotFound; if present
// but in the wrong status → wrongStatusErr.
// This keeps the happy path single-query while giving callers a precise error.
func statusOrNotFound(
	ctx context.Context,
	dbtx db.DBTX,
	tenantID, versionID uuid.UUID,
	wrongStatusErr error,
) error {
	_, err := db.New(dbtx).GetWorkflowVersionByID(ctx, db.GetWorkflowVersionByIDParams{
		TenantID: tenantID,
		ID:       versionID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	return wrongStatusErr
}

func statusOrNotFoundOrConcurrency(
	ctx context.Context,
	dbtx db.DBTX,
	tenantID, versionID uuid.UUID,
) error {
	row, err := db.New(dbtx).GetWorkflowVersionByID(ctx, db.GetWorkflowVersionByIDParams{
		TenantID: tenantID,
		ID:       versionID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	if fromVersionStatus(row.Status) != domain.VersionStatusDraft {
		return domain.ErrVersionNotDraft
	}
	return domain.ErrDraftConcurrency
}
