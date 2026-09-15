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

// statusOrNotFound distinguishes "version missing" (ErrNotFound) from "version
// exists but in the wrong status" (wrongStatusErr), without an extra happy-path query.
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
