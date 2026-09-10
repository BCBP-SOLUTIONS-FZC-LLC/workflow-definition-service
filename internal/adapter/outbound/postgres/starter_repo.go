package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgcommon"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres/db"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

var _ port.StarterTemplateRepository = (*StarterRepo)(nil)

type StarterRepo struct {
	pool *pgcommon.Pool
}

func NewStarterRepo(pool *pgcommon.Pool) *StarterRepo {
	return &StarterRepo{pool: pool}
}

func (r *StarterRepo) Create(ctx context.Context, s *domain.StarterTemplate) error {
	return exec(ctx, r.pool, func(dbtx db.DBTX) error {
		return mapErr(db.New(dbtx).CreateStarter(ctx, db.CreateStarterParams{
			ID:                      s.ID,
			TenantID:                toPgtypeUUID(s.TenantID),
			Scope:                   toCatalogScope(s.Scope),
			Name:                    s.Name,
			Description:             toNullableText(s.Description),
			Category:                toNullableText(s.Category),
			BpmnXml:                 s.BPMNXML,
			SourceWorkflowVersionID: toPgtypeUUID(s.SourceWorkflowVersionID),
			CreatedByUserID:         toPgtypeUUID(s.CreatedByUserID),
		}))
	})
}

func (r *StarterRepo) GetByID(ctx context.Context, callerTenantID, id uuid.UUID) (*domain.StarterTemplate, error) {
	var result *domain.StarterTemplate
	err := exec(ctx, r.pool, func(dbtx db.DBTX) error {
		row, err := db.New(dbtx).GetStarterByID(ctx, db.GetStarterByIDParams{
			ID:       id,
			TenantID: pgtype.UUID{Bytes: callerTenantID, Valid: true},
		})
		if err != nil {
			return mapErr(err)
		}
		result = starterFromDB(row)
		return nil
	})
	return result, err
}

func (r *StarterRepo) Delete(ctx context.Context, id uuid.UUID) error {
	return exec(ctx, r.pool, func(dbtx db.DBTX) error {
		tag, err := db.New(dbtx).DeleteStarter(ctx, id)
		if err != nil {
			return mapErr(err)
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrNotFound
		}
		return nil
	})
}

func (r *StarterRepo) List(
	ctx context.Context,
	callerTenantID uuid.UUID,
	f port.StarterFilter,
) ([]*domain.StarterTemplate, int64, error) {
	var (
		results []*domain.StarterTemplate
		total   int64
	)
	err := exec(ctx, r.pool, func(dbtx db.DBTX) error {
		var err error
		results, total, err = listStarters(ctx, dbtx, callerTenantID, f)
		return err
	})
	return results, total, err
}

func listStarters(
	ctx context.Context,
	dbtx db.DBTX,
	callerTenantID uuid.UUID,
	f port.StarterFilter,
) ([]*domain.StarterTemplate, int64, error) {
	args := []any{callerTenantID}
	argN := func() string { return fmt.Sprintf("$%d", len(args)) }
	nextArg := func(v interface{}) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}

	var where strings.Builder
	fmt.Fprintf(&where, "WHERE (scope = 'global' OR tenant_id = %s)\n", argN())
	if f.Scope != nil {
		fmt.Fprintf(&where, "  AND scope = %s\n", nextArg(string(*f.Scope)))
	}
	if f.Category != nil {
		fmt.Fprintf(&where, "  AND category = %s\n", nextArg(*f.Category))
	}
	if f.Search != nil {
		fmt.Fprintf(&where, "  AND name ILIKE %s\n", nextArg("%"+*f.Search+"%"))
	}

	countSQL := fmt.Sprintf("SELECT COUNT(*) FROM workflow_template\n%s", where.String())
	var total int64
	if err := dbtx.QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("list starters count: %w", err)
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
		`SELECT id, tenant_id, scope, name, description, category, bpmn_xml,
		        source_workflow_version_id, created_by_user_id, created_at, updated_at, record_version
		 FROM workflow_template
		 %sORDER BY created_at DESC
		 LIMIT %s OFFSET %s`,
		where.String(), nextArg(int32(limit)), nextArg(int32(offset)),
	)

	rows, err := dbtx.Query(ctx, dataSQL, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list starters: %w", err)
	}
	defer rows.Close()

	var results []*domain.StarterTemplate
	for rows.Next() {
		var row db.GetStarterByIDRow
		if err := rows.Scan(
			&row.ID, &row.TenantID, &row.Scope, &row.Name, &row.Description, &row.Category, &row.BpmnXml,
			&row.SourceWorkflowVersionID, &row.CreatedByUserID, &row.CreatedAt, &row.UpdatedAt, &row.RecordVersion,
		); err != nil {
			return nil, 0, fmt.Errorf("list starters scan: %w", err)
		}
		results = append(results, starterFromDB(row))
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("list starters rows: %w", err)
	}
	return results, total, nil
}
