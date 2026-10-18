package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	pgdomain "github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres/db"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

var (
	_ port.TenantRetentionRepository = (*TenantRetentionRepo)(nil)
	_ port.TenantDataPurger          = (*TenantRetentionRepo)(nil)
)

type TenantRetentionRepo struct {
	pool *pgcommon.Pool
}

func NewTenantRetentionRepo(pool *pgcommon.Pool) *TenantRetentionRepo {
	return &TenantRetentionRepo{pool: pool}
}

func (r *TenantRetentionRepo) RecordOffboarded(ctx context.Context, tenantID uuid.UUID, offboardedAt, purgeAfter time.Time) error {
	return exec(ctx, r.pool, func(dbtx db.DBTX) error {
		return db.New(dbtx).RecordTenantOffboarded(ctx, db.RecordTenantOffboardedParams{
			TenantID: tenantID, OffboardedAt: offboardedAt, ArtifactsPurgeAfter: purgeAfter,
		})
	})
}

func (r *TenantRetentionRepo) ListDue(ctx context.Context, now time.Time, limit int) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	err := exec(ctx, r.pool, func(dbtx db.DBTX) error {
		rows, err := db.New(dbtx).ListTenantsDueForPurge(ctx, db.ListTenantsDueForPurgeParams{ArtifactsPurgeAfter: now, Limit: int32(limit)})
		if err != nil {
			return err
		}
		ids = rows
		return nil
	})
	return ids, err
}

func (r *TenantRetentionRepo) MarkPurged(ctx context.Context, tenantID uuid.UUID, at time.Time) error {
	return exec(ctx, r.pool, func(dbtx db.DBTX) error {
		return db.New(dbtx).MarkTenantPurged(ctx, db.MarkTenantPurgedParams{TenantID: tenantID, PurgedAt: pgtype.Timestamptz{Time: at, Valid: true}})
	})
}

type purgeStep struct {
	table string
	run   func(ctx context.Context, q *db.Queries, tenantID uuid.UUID, limit int32) (int64, error)
}

var tenantPurgeSteps = []purgeStep{
	{"workflow", func(ctx context.Context, q *db.Queries, t uuid.UUID, n int32) (int64, error) {
		return q.PurgeTenantWorkflows(ctx, db.PurgeTenantWorkflowsParams{TenantID: t, Limit: n})
	}},
	{"workflow_module", func(ctx context.Context, q *db.Queries, t uuid.UUID, n int32) (int64, error) {
		return q.PurgeTenantModules(ctx, db.PurgeTenantModulesParams{TenantID: pgtype.UUID{Bytes: t, Valid: true}, Limit: n})
	}},
	{"workflow_template", func(ctx context.Context, q *db.Queries, t uuid.UUID, n int32) (int64, error) {
		return q.PurgeTenantStarters(ctx, db.PurgeTenantStartersParams{TenantID: pgtype.UUID{Bytes: t, Valid: true}, Limit: n})
	}},
}

func (r *TenantRetentionRepo) PurgeBatch(ctx context.Context, tenantID uuid.UUID, limit int) (map[string]int64, error) {
	deleted := map[string]int64{}
	ctx = pgcommon.WithGUCSet(ctx, pgdomain.GUCSet{TenantID: tenantID.String()})
	err := exec(ctx, r.pool, func(dbtx db.DBTX) error {
		q := db.New(dbtx)
		for _, step := range tenantPurgeSteps {
			n, err := step.run(ctx, q, tenantID, int32(limit))
			if err != nil {
				return fmt.Errorf("purge %s: %w", step.table, err)
			}
			if n > 0 {
				deleted[step.table] = n
				return nil
			}
		}
		return nil
	})
	return deleted, err
}
