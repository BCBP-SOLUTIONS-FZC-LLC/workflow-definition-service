package postgres

import (
	"context"
	"errors"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgcommon"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres/db"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

var _ port.AssigneeRepository = (*AssigneeRepo)(nil)

type AssigneeRepo struct {
	pool *pgcommon.Pool
}

func NewAssigneeRepo(pool *pgcommon.Pool) *AssigneeRepo {
	return &AssigneeRepo{pool: pool}
}

func (r *AssigneeRepo) BulkInsert(
	ctx context.Context,
	tenantID, versionID uuid.UUID,
	assignees []*domain.NodeAssignee,
) error {
	if len(assignees) == 0 {
		return nil
	}
	return exec(ctx, r.pool, func(dbtx db.DBTX) error {
		params := make([]db.InsertAssigneeParams, len(assignees))
		for i, a := range assignees {
			params[i] = db.InsertAssigneeParams{
				ID:                a.ID,
				TenantID:          tenantID,
				WorkflowVersionID: pgtype.UUID{Bytes: versionID, Valid: true},
				NodeKey:           a.NodeKey,
				UserID:            a.UserID,
				DepartmentID:      a.DepartmentID,
				Role:              a.Role,
			}
		}
		br := db.New(dbtx).InsertAssignee(ctx, params)
		var errs []error
		br.Exec(func(_ int, err error) {
			if err != nil {
				errs = append(errs, mapErr(err))
			}
		})
		if closeErr := br.Close(); closeErr != nil {
			errs = append(errs, closeErr)
		}
		return errors.Join(errs...)
	})
}

func (r *AssigneeRepo) ListByUser(
	ctx context.Context,
	tenantID, userID uuid.UUID,
) ([]*domain.NodeAssignee, error) {
	var results []*domain.NodeAssignee
	err := exec(ctx, r.pool, func(dbtx db.DBTX) error {
		rows, err := db.New(dbtx).ListAssigneesByUser(ctx, db.ListAssigneesByUserParams{
			TenantID: tenantID,
			UserID:   userID,
		})
		if err != nil {
			return mapErr(err)
		}
		results = make([]*domain.NodeAssignee, 0, len(rows))
		for _, row := range rows {
			results = append(results, assigneeFromDB(row))
		}
		return nil
	})
	return results, err
}

func (r *AssigneeRepo) DeleteByVersion(ctx context.Context, tenantID, versionID uuid.UUID) error {
	return exec(ctx, r.pool, func(dbtx db.DBTX) error {
		return mapErr(db.New(dbtx).DeleteAssigneesByVersion(ctx, db.DeleteAssigneesByVersionParams{
			TenantID:          tenantID,
			WorkflowVersionID: pgtype.UUID{Bytes: versionID, Valid: true},
		}))
	})
}
