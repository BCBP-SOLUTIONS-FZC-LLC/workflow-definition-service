package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgcommon"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres/db"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

var _ port.ModuleVersionRepository = (*ModuleVersionRepo)(nil)

type ModuleVersionRepo struct {
	pool *pgcommon.Pool
}

func NewModuleVersionRepo(pool *pgcommon.Pool) *ModuleVersionRepo {
	return &ModuleVersionRepo{pool: pool}
}

func (r *ModuleVersionRepo) Create(ctx context.Context, v *domain.ModuleVersion) error {
	return exec(ctx, r.pool, func(dbtx db.DBTX) error {
		return mapErr(db.New(dbtx).CreateModuleVersion(ctx, db.CreateModuleVersionParams{
			ID:                   v.ID,
			ModuleID:             pgtype.UUID{Bytes: v.ModuleID, Valid: true},
			TenantID:             toPgtypeUUID(v.TenantID),
			Scope:                toCatalogScope(v.Scope),
			Status:               toVersionStatus(v.Status),
			BpmnXml:              v.BPMNXML,
			ProcessID:            v.ProcessID,
			VersionNumber:        toPgtypeInt4(v.VersionNumber),
			IsValid:              v.IsValid,
			ValidationErrorsJson: toNullableJSONB(v.ValidationErrorsJSON),
			CreatedByUserID:      toPgtypeUUID(v.CreatedByUserID),
		}))
	})
}

func (r *ModuleVersionRepo) GetByID(ctx context.Context, callerTenantID, id uuid.UUID) (*domain.ModuleVersion, error) {
	var result *domain.ModuleVersion
	err := exec(ctx, r.pool, func(dbtx db.DBTX) error {
		row, err := db.New(dbtx).GetModuleVersionByID(ctx, db.GetModuleVersionByIDParams{
			ID:       id,
			TenantID: pgtype.UUID{Bytes: callerTenantID, Valid: true},
		})
		if err != nil {
			return mapErr(err)
		}
		result = moduleVersionFromDB(row)
		return nil
	})
	return result, err
}

func (r *ModuleVersionRepo) ListByModule(
	ctx context.Context,
	callerTenantID, moduleID uuid.UUID,
	page, limit int,
) ([]*domain.ModuleVersion, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 20
	}
	offset := int32((page - 1) * limit)

	var (
		results []*domain.ModuleVersion
		total   int64
	)
	err := exec(ctx, r.pool, func(dbtx db.DBTX) error {
		q := db.New(dbtx)
		params := db.CountModuleVersionsByModuleParams{
			ModuleID: pgtype.UUID{Bytes: moduleID, Valid: true},
			TenantID: pgtype.UUID{Bytes: callerTenantID, Valid: true},
		}
		var err error
		total, err = q.CountModuleVersionsByModule(ctx, params)
		if err != nil {
			return mapErr(err)
		}
		rows, err := q.ListModuleVersionsByModule(ctx, db.ListModuleVersionsByModuleParams{
			ModuleID: params.ModuleID,
			TenantID: params.TenantID,
			Limit:    int32(limit),
			Offset:   offset,
		})
		if err != nil {
			return mapErr(err)
		}
		results = make([]*domain.ModuleVersion, 0, len(rows))
		for _, row := range rows {
			results = append(results, moduleVersionFromDB(db.GetModuleVersionByIDRow(row)))
		}
		return nil
	})
	return results, total, err
}

func (r *ModuleVersionRepo) Publish(ctx context.Context, versionID uuid.UUID, versionNumber int32) error {
	return exec(ctx, r.pool, func(dbtx db.DBTX) error {
		tag, err := db.New(dbtx).PublishModuleVersion(ctx, db.PublishModuleVersionParams{
			ID:            versionID,
			VersionNumber: pgtype.Int4{Int32: versionNumber, Valid: true},
		})
		if err != nil {
			return mapErr(err)
		}
		if tag.RowsAffected() == 0 {
			return moduleVersionStatusOrNotFound(ctx, dbtx, versionID, domain.ErrVersionNotDraft)
		}
		return nil
	})
}

func (r *ModuleVersionRepo) Archive(ctx context.Context, versionID uuid.UUID) error {
	return exec(ctx, r.pool, func(dbtx db.DBTX) error {
		tag, err := db.New(dbtx).ArchiveModuleVersion(ctx, versionID)
		if err != nil {
			return mapErr(err)
		}
		if tag.RowsAffected() == 0 {
			return moduleVersionStatusOrNotFound(ctx, dbtx, versionID, domain.ErrVersionNotPublished)
		}
		return nil
	})
}

func (r *ModuleVersionRepo) NextVersionNumber(ctx context.Context, moduleID uuid.UUID) (int32, error) {
	var n int32
	err := exec(ctx, r.pool, func(dbtx db.DBTX) error {
		var err error
		n, err = db.New(dbtx).NextModuleVersionNumber(ctx, pgtype.UUID{Bytes: moduleID, Valid: true})
		return mapErr(err)
	})
	return n, err
}

func moduleVersionStatusOrNotFound(ctx context.Context, dbtx db.DBTX, versionID uuid.UUID, wrongStatusErr error) error {
	var status string
	err := dbtx.QueryRow(ctx, `SELECT status FROM workflow_module_version WHERE id = $1`, versionID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("module version status lookup: %w", err)
	}
	return wrongStatusErr
}
