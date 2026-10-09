package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
)

func TestRLSViolationRepo_CountsAfterAndPrune(t *testing.T) {
	pool := newTestPool(t)
	repo := pgadapter.NewRLSViolationRepo(pool)
	ctx := context.Background()

	insert := func(violationType string, age time.Duration) {
		t.Helper()
		err := pool.WithConn(ctx, func(ctx context.Context, conn *pgxpool.Conn) error {
			_, err := conn.Exec(ctx,
				`INSERT INTO rls_violation_log (violation_type, row_tenant_id, table_name, occurred_at)
				 VALUES ($1, gen_random_uuid(), 'workflow', now() - $2::interval)`,
				violationType, age.String())
			return err
		})
		if err != nil {
			t.Fatalf("insert violation: %v", err)
		}
	}

	insert("cross_tenant_access", 0)
	start, err := repo.LatestID(ctx)
	if err != nil {
		t.Fatalf("LatestID: %v", err)
	}
	insert("cross_tenant_access", 0)
	insert("cross_tenant_access", 0)
	insert("missing_or_invalid_guc", 0)

	counts, newest, err := repo.CountsAfter(ctx, start)
	if err != nil {
		t.Fatalf("CountsAfter: %v", err)
	}
	got := map[string]int64{}
	for _, c := range counts {
		got[c.ViolationType] = c.Count
	}
	if got["cross_tenant_access"] != 2 || got["missing_or_invalid_guc"] != 1 {
		t.Errorf("counts after start = %v, want cross_tenant_access 2 and missing_or_invalid_guc 1", got)
	}
	if newest <= start {
		t.Errorf("newest id = %d, want > %d", newest, start)
	}

	again, same, err := repo.CountsAfter(ctx, newest)
	if err != nil || len(again) != 0 || same != newest {
		t.Errorf("CountsAfter(newest) = %v, %d, %v; want no rows and the same position", again, same, err)
	}

	insert("cross_tenant_access", 48*time.Hour)
	pruned, err := repo.PruneBefore(ctx, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("PruneBefore: %v", err)
	}
	if pruned != 1 {
		t.Errorf("pruned = %d, want only the 48h-old row", pruned)
	}
}
