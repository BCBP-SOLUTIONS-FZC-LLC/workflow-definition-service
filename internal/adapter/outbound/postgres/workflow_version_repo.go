package postgres

import (
	"context"
	"encoding/json"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgcommon"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres/db"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

var _ port.WorkflowVersionRepository = (*WorkflowVersionRepo)(nil)

type WorkflowVersionRepo struct {
	pool *pgcommon.Pool
}

func NewWorkflowVersionRepo(pool *pgcommon.Pool) *WorkflowVersionRepo {
	return &WorkflowVersionRepo{pool: pool}
}

func (r *WorkflowVersionRepo) Create(ctx context.Context, v *domain.WorkflowVersion) error {
	return exec(ctx, r.pool, func(dbtx db.DBTX) error {
		return mapErr(db.New(dbtx).CreateWorkflowVersion(ctx, db.CreateWorkflowVersionParams{
			ID:                   v.ID,
			WorkflowID:           pgtype.UUID{Bytes: v.WorkflowID, Valid: true},
			TenantID:             v.TenantID,
			Status:               toVersionStatus(v.Status),
			BpmnXml:              v.BPMNXML,
			CompiledPlanJson:     toNullableJSONB(v.CompiledPlanJSON),
			ArtifactHash:         toNullableText(v.ArtifactHash),
			VersionNumber:        toPgtypeInt4(v.VersionNumber),
			PublishedAt:          toPgtypeTimestamp(v.PublishedAt),
			CreatedByUserID:      v.CreatedByUserID,
			IsValid:              v.IsValid,
			ValidationErrorsJson: toNullableJSONB(v.ValidationErrorsJSON),
			ModuleBpmnXmls:       coalesceStrings(v.ModuleBPMNXMLs),
		}))
	})
}

func (r *WorkflowVersionRepo) GetByID(ctx context.Context, tenantID, id uuid.UUID) (*domain.WorkflowVersion, error) {
	var result *domain.WorkflowVersion
	err := exec(ctx, r.pool, func(dbtx db.DBTX) error {
		row, err := db.New(dbtx).GetWorkflowVersionByID(ctx, db.GetWorkflowVersionByIDParams{
			TenantID: tenantID,
			ID:       id,
		})
		if err != nil {
			return mapErr(err)
		}
		result = workflowVersionFromDB(row)
		return nil
	})
	return result, err
}

func (r *WorkflowVersionRepo) GetDraft(
	ctx context.Context,
	tenantID, workflowID uuid.UUID,
) (*domain.WorkflowVersion, error) {
	var result *domain.WorkflowVersion
	err := exec(ctx, r.pool, func(dbtx db.DBTX) error {
		row, err := db.New(dbtx).GetDraftVersion(ctx, db.GetDraftVersionParams{
			TenantID:   tenantID,
			WorkflowID: pgtype.UUID{Bytes: workflowID, Valid: true},
		})
		if err != nil {
			return mapErr(err)
		}
		result = workflowVersionFromDB(row)
		return nil
	})
	return result, err
}

func (r *WorkflowVersionRepo) ListByWorkflow(
	ctx context.Context,
	tenantID, workflowID uuid.UUID,
	page, limit int,
) ([]*domain.WorkflowVersion, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 20
	}
	offset := int32((page - 1) * limit)

	var (
		results []*domain.WorkflowVersion
		total   int64
	)
	err := exec(ctx, r.pool, func(dbtx db.DBTX) error {
		q := db.New(dbtx)
		var err error
		total, err = q.CountVersionsByWorkflow(ctx, db.CountVersionsByWorkflowParams{
			TenantID:   tenantID,
			WorkflowID: pgtype.UUID{Bytes: workflowID, Valid: true},
		})
		if err != nil {
			return mapErr(err)
		}
		rows, err := q.ListVersionsByWorkflow(ctx, db.ListVersionsByWorkflowParams{
			TenantID:   tenantID,
			WorkflowID: pgtype.UUID{Bytes: workflowID, Valid: true},
			Limit:      int32(limit),
			Offset:     offset,
		})
		if err != nil {
			return mapErr(err)
		}
		results = make([]*domain.WorkflowVersion, 0, len(rows))
		for _, row := range rows {
			results = append(results, workflowVersionFromDB(row))
		}
		return nil
	})
	return results, total, err
}

func (r *WorkflowVersionRepo) UpdateDraft(ctx context.Context, v *domain.WorkflowVersion) error {
	return exec(ctx, r.pool, func(dbtx db.DBTX) error {
		tag, err := db.New(dbtx).UpdateDraftVersion(ctx, db.UpdateDraftVersionParams{
			TenantID:             v.TenantID,
			ID:                   v.ID,
			BpmnXml:              v.BPMNXML,
			CompiledPlanJson:     toNullableJSONB(v.CompiledPlanJSON),
			ArtifactHash:         toNullableText(v.ArtifactHash),
			IsValid:              v.IsValid,
			ValidationErrorsJson: toNullableJSONB(v.ValidationErrorsJSON),
			RecordVersion:        v.RecordVersion,
			ModuleBpmnXmls:       coalesceStrings(v.ModuleBPMNXMLs),
		})
		if err != nil {
			return mapErr(err)
		}
		if tag.RowsAffected() == 0 {
			return statusOrNotFoundOrConcurrency(ctx, dbtx, v.TenantID, v.ID)
		}
		v.RecordVersion++ // mirror the DB trigger bump so the caller holds the current token without a re-fetch
		return nil
	})
}

func (r *WorkflowVersionRepo) Publish(
	ctx context.Context,
	tenantID, versionID uuid.UUID,
	versionNumber int32,
	compiledPlanJSON, artifactHash string,
) error {
	return exec(ctx, r.pool, func(dbtx db.DBTX) error {
		tag, err := db.New(dbtx).PublishVersion(ctx, db.PublishVersionParams{
			TenantID:         tenantID,
			ID:               versionID,
			VersionNumber:    pgtype.Int4{Int32: versionNumber, Valid: true},
			CompiledPlanJson: json.RawMessage(compiledPlanJSON),
			ArtifactHash:     toNullableText(artifactHash),
		})
		if err != nil {
			return mapErr(err)
		}
		if tag.RowsAffected() == 0 {
			return statusOrNotFound(ctx, dbtx, tenantID, versionID, domain.ErrVersionNotDraft)
		}
		return nil
	})
}

func (r *WorkflowVersionRepo) Archive(ctx context.Context, tenantID, versionID uuid.UUID) error {
	return exec(ctx, r.pool, func(dbtx db.DBTX) error {
		tag, err := db.New(dbtx).ArchiveVersion(ctx, db.ArchiveVersionParams{
			TenantID: tenantID,
			ID:       versionID,
		})
		if err != nil {
			return mapErr(err)
		}
		if tag.RowsAffected() == 0 {
			return statusOrNotFound(ctx, dbtx, tenantID, versionID, domain.ErrVersionNotPublished)
		}
		return nil
	})
}

func (r *WorkflowVersionRepo) DeleteDraft(ctx context.Context, tenantID, versionID uuid.UUID) error {
	return exec(ctx, r.pool, func(dbtx db.DBTX) error {
		tag, err := db.New(dbtx).DeleteDraftVersion(ctx, db.DeleteDraftVersionParams{
			TenantID: tenantID,
			ID:       versionID,
		})
		if err != nil {
			return mapErr(err)
		}
		if tag.RowsAffected() == 0 {
			return statusOrNotFound(ctx, dbtx, tenantID, versionID, domain.ErrVersionNotDraft)
		}
		return nil
	})
}

func (r *WorkflowVersionRepo) SetInvalid(
	ctx context.Context,
	tenantID, versionID uuid.UUID,
	errorsJSON string,
) error {
	return exec(ctx, r.pool, func(dbtx db.DBTX) error {
		tag, err := db.New(dbtx).SetVersionInvalid(ctx, db.SetVersionInvalidParams{
			TenantID:             tenantID,
			ID:                   versionID,
			ValidationErrorsJson: json.RawMessage(errorsJSON),
		})
		if err != nil {
			return mapErr(err)
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrNotFound
		}
		return nil
	})
}

func (r *WorkflowVersionRepo) NextVersionNumber(
	ctx context.Context,
	tenantID, workflowID uuid.UUID,
) (int32, error) {
	var n int32
	err := exec(ctx, r.pool, func(dbtx db.DBTX) error {
		var err error
		n, err = db.New(dbtx).NextVersionNumber(ctx, db.NextVersionNumberParams{
			TenantID:   tenantID,
			WorkflowID: pgtype.UUID{Bytes: workflowID, Valid: true},
		})
		return mapErr(err)
	})
	return n, err
}
