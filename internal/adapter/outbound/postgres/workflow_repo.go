package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgcommon"
	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres/db"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

var _ port.WorkflowRepository = (*WorkflowRepo)(nil)

type WorkflowRepo struct {
	pool *pgcommon.Pool
}

func NewWorkflowRepo(pool *pgcommon.Pool) *WorkflowRepo {
	return &WorkflowRepo{pool: pool}
}

func (r *WorkflowRepo) Create(ctx context.Context, w *domain.Workflow) error {
	return exec(ctx, r.pool, func(dbtx db.DBTX) error {
		return mapErr(db.New(dbtx).CreateWorkflow(ctx, db.CreateWorkflowParams{
			ID:              w.ID,
			TenantID:        w.TenantID,
			CreatedByUserID: w.CreatedByUserID,
			BusinessKey:     w.BusinessKey,
			Name:            w.Name,
			Description:     toNullableText(w.Description),
			ActiveVersionID: toPgtypeUUID(w.ActiveVersionID),
		}))
	})
}

func (r *WorkflowRepo) GetByID(
	ctx context.Context,
	tenantID, id uuid.UUID,
) (*domain.Workflow, error) {
	var result *domain.Workflow
	err := exec(ctx, r.pool, func(dbtx db.DBTX) error {
		row, err := db.New(dbtx).GetWorkflowByID(ctx, db.GetWorkflowByIDParams{
			TenantID: tenantID,
			ID:       id,
		})
		if err != nil {
			return mapErr(err)
		}
		result = workflowFromDB(row)
		return nil
	})
	return result, err
}

func (r *WorkflowRepo) GetByBusinessKey(
	ctx context.Context,
	tenantID uuid.UUID,
	businessKey string,
) (*domain.Workflow, error) {
	var result *domain.Workflow
	err := exec(ctx, r.pool, func(dbtx db.DBTX) error {
		row, err := db.New(dbtx).GetWorkflowByBusinessKey(ctx, db.GetWorkflowByBusinessKeyParams{
			TenantID:    tenantID,
			BusinessKey: businessKey,
		})
		if err != nil {
			return mapErr(err)
		}
		result = workflowFromDB(row)
		return nil
	})
	return result, err
}

func (r *WorkflowRepo) UpdateActiveVersion(
	ctx context.Context,
	tenantID, workflowID uuid.UUID,
	versionID *uuid.UUID,
) error {
	return exec(ctx, r.pool, func(dbtx db.DBTX) error {
		tag, err := db.New(dbtx).UpdateActiveVersion(ctx, db.UpdateActiveVersionParams{
			TenantID:        tenantID,
			ID:              workflowID,
			ActiveVersionID: toPgtypeUUID(versionID),
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

func (r *WorkflowRepo) CountByTenant(ctx context.Context, tenantID uuid.UUID) (int64, error) {
	var count int64
	err := exec(ctx, r.pool, func(dbtx db.DBTX) error {
		var err error
		count, err = db.New(dbtx).CountWorkflowsByTenant(ctx, tenantID)
		return mapErr(err)
	})
	return count, err
}

// List executes a dynamic query supporting optional filter fields.
// A LEFT JOIN on the active version is added when the IsValid filter is set.
// A LEFT JOIN on draft versions is added when the HasDraft filter is set.
// Two queries run: COUNT(*) for pagination, then the data page.
func (r *WorkflowRepo) List(
	ctx context.Context,
	tenantID uuid.UUID,
	f port.WorkflowFilter,
) ([]*domain.Workflow, int64, error) {
	var (
		results []*domain.Workflow
		total   int64
	)
	err := exec(ctx, r.pool, func(dbtx db.DBTX) error {
		var err error
		results, total, err = listWorkflows(ctx, dbtx, tenantID, f)
		return err
	})
	return results, total, err
}

func listWorkflows(
	ctx context.Context,
	dbtx db.DBTX,
	tenantID uuid.UUID,
	f port.WorkflowFilter,
) ([]*domain.Workflow, int64, error) {
	needsActiveJoin := f.IsValid != nil
	needsDraftJoin := f.HasDraft != nil

	// args holds all query parameters in order. argN() reads the current $N
	// placeholder for the last-appended value; nextArg(v) appends v and
	// returns its $N. Never build a placeholder manually — always use these
	// two helpers to keep args and $N numbering in sync.
	args := []interface{}{tenantID}
	argN := func() string {
		n := fmt.Sprintf("$%d", len(args))
		return n
	}
	nextArg := func(v interface{}) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}

	var joins strings.Builder
	var where strings.Builder
	fmt.Fprintf(&where, "WHERE w.tenant_id = %s\n", argN())

	if needsActiveJoin {
		joins.WriteString("LEFT JOIN workflow_version wv_active ON wv_active.id = w.active_version_id\n")
	}
	if needsDraftJoin {
		joins.WriteString(
			"LEFT JOIN workflow_version wv_draft ON wv_draft.workflow_id = w.id AND wv_draft.status = 'DRAFT'\n",
		)
	}
	if f.Search != nil {
		p := nextArg("%" + *f.Search + "%")
		fmt.Fprintf(&where, "  AND w.name ILIKE %s\n", p)
	}
	if f.BusinessKey != nil {
		fmt.Fprintf(&where, "  AND w.business_key = %s\n", nextArg(*f.BusinessKey))
	}
	if f.Archived != nil {
		if *f.Archived {
			where.WriteString("  AND w.active_version_id IS NULL\n")
		} else {
			where.WriteString("  AND w.active_version_id IS NOT NULL\n")
		}
	}
	if f.IsValid != nil {
		fmt.Fprintf(&where, "  AND wv_active.is_valid = %s\n", nextArg(*f.IsValid))
	}
	if f.HasDraft != nil {
		if *f.HasDraft {
			where.WriteString("  AND wv_draft.id IS NOT NULL\n")
		} else {
			where.WriteString("  AND wv_draft.id IS NULL\n")
		}
	}

	countSQL := fmt.Sprintf(
		"SELECT COUNT(*) FROM workflow w\n%s%s",
		joins.String(), where.String(),
	)
	var total int64
	countRow := dbtx.QueryRow(ctx, countSQL, args...)
	if err := countRow.Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("list workflows count: %w", err)
	}

	page := f.Page
	if page < 1 {
		page = 1
	}
	limit := f.Limit
	if limit < 1 {
		limit = 20
	}
	offset := (page - 1) * limit

	dataSQL := fmt.Sprintf(
		`SELECT w.id, w.tenant_id, w.created_by_user_id, w.business_key,
		        w.name, w.description, w.active_version_id, w.created_at, w.updated_at
		 FROM workflow w
		 %s%sORDER BY w.created_at DESC
		 LIMIT %s OFFSET %s`,
		joins.String(), where.String(),
		nextArg(int32(limit)), nextArg(int32(offset)),
	)

	rows, err := dbtx.Query(ctx, dataSQL, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list workflows: %w", err)
	}
	defer rows.Close()

	var results []*domain.Workflow
	for rows.Next() {
		var row db.Workflow
		if err := rows.Scan(
			&row.ID, &row.TenantID, &row.CreatedByUserID, &row.BusinessKey,
			&row.Name, &row.Description, &row.ActiveVersionID, &row.CreatedAt, &row.UpdatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("list workflows scan: %w", err)
		}
		results = append(results, workflowFromDB(row))
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("list workflows rows: %w", err)
	}
	return results, total, nil
}
