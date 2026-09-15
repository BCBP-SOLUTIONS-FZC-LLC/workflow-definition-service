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

var _ port.ModuleRepository = (*ModuleRepo)(nil)

type ModuleRepo struct {
	pool *pgcommon.Pool
}

func NewModuleRepo(pool *pgcommon.Pool) *ModuleRepo {
	return &ModuleRepo{pool: pool}
}

func (r *ModuleRepo) Create(ctx context.Context, m *domain.Module) error {
	return exec(ctx, r.pool, func(dbtx db.DBTX) error {
		return mapErr(db.New(dbtx).CreateModule(ctx, db.CreateModuleParams{
			ID:              m.ID,
			TenantID:        toPgtypeUUID(m.TenantID),
			Scope:           toCatalogScope(m.Scope),
			Name:            m.Name,
			Description:     toNullableText(m.Description),
			CreatedByUserID: toPgtypeUUID(m.CreatedByUserID),
		}))
	})
}

func (r *ModuleRepo) GetByID(ctx context.Context, callerTenantID, id uuid.UUID) (*domain.Module, error) {
	var result *domain.Module
	err := exec(ctx, r.pool, func(dbtx db.DBTX) error {
		row, err := db.New(dbtx).GetModuleByID(ctx, db.GetModuleByIDParams{
			ID:       id,
			TenantID: pgtype.UUID{Bytes: callerTenantID, Valid: true},
		})
		if err != nil {
			return mapErr(err)
		}
		result = moduleFromDB(row)
		return nil
	})
	return result, err
}

func (r *ModuleRepo) UpdateActiveVersion(ctx context.Context, moduleID uuid.UUID, versionID *uuid.UUID) error {
	return exec(ctx, r.pool, func(dbtx db.DBTX) error {
		tag, err := db.New(dbtx).UpdateModuleActiveVersion(ctx, db.UpdateModuleActiveVersionParams{
			ID:              moduleID,
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

func (r *ModuleRepo) List(
	ctx context.Context,
	callerTenantID uuid.UUID,
	f port.ModuleFilter,
) ([]*domain.Module, int64, error) {
	var (
		results []*domain.Module
		total   int64
	)
	err := exec(ctx, r.pool, func(dbtx db.DBTX) error {
		var err error
		results, total, err = listModules(ctx, dbtx, callerTenantID, f)
		return err
	})
	return results, total, err
}

func listModules(
	ctx context.Context,
	dbtx db.DBTX,
	callerTenantID uuid.UUID,
	f port.ModuleFilter,
) ([]*domain.Module, int64, error) {
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
	if f.Search != nil {
		fmt.Fprintf(&where, "  AND name ILIKE %s\n", nextArg("%"+*f.Search+"%"))
	}

	countSQL := fmt.Sprintf("SELECT COUNT(*) FROM workflow_module\n%s", where.String())
	var total int64
	if err := dbtx.QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("list modules count: %w", err)
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
		`SELECT id, tenant_id, scope, name, description, active_version_id,
		        created_by_user_id, created_at, updated_at, record_version
		 FROM workflow_module
		 %sORDER BY created_at DESC
		 LIMIT %s OFFSET %s`,
		where.String(), nextArg(int32(limit)), nextArg(int32(offset)),
	)

	rows, err := dbtx.Query(ctx, dataSQL, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list modules: %w", err)
	}
	defer rows.Close()

	var results []*domain.Module
	for rows.Next() {
		var row db.GetModuleByIDRow
		if err := rows.Scan(
			&row.ID, &row.TenantID, &row.Scope, &row.Name, &row.Description,
			&row.ActiveVersionID, &row.CreatedByUserID, &row.CreatedAt, &row.UpdatedAt, &row.RecordVersion,
		); err != nil {
			return nil, 0, fmt.Errorf("list modules scan: %w", err)
		}
		results = append(results, moduleFromDB(row))
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("list modules rows: %w", err)
	}
	return results, total, nil
}
