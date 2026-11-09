//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestRLSViolationRepo_PruneBefore_FailureClassification(t *testing.T) {
	cases := []struct {
		name            string
		triggerSQLState string
		wantUnavailable bool
	}{
		{name: "pool already closed", wantUnavailable: true},
		{name: "delete cancelled by the server", triggerSQLState: "57014", wantUnavailable: true},
		{name: "delete refused by a constraint", triggerSQLState: "23514", wantUnavailable: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := newTestPool(t)
			repo := pgadapter.NewRLSViolationRepo(pool)
			ctx := context.Background()
			if tc.triggerSQLState == "" {
				pool.Close()
			} else {
				require.NoError(t, pool.WithConn(ctx, func(ctx context.Context, conn *pgxpool.Conn) error {
					_, err := conn.Exec(ctx, `
						INSERT INTO rls_violation_log (violation_type, row_tenant_id, table_name, occurred_at)
						VALUES ('cross_tenant_access', gen_random_uuid(), 'workflow', now() - interval '10 days');
						CREATE FUNCTION prune_probe_fn() RETURNS trigger LANGUAGE plpgsql AS $$
						BEGIN RAISE EXCEPTION 'prune probe' USING ERRCODE = '`+tc.triggerSQLState+`'; END $$;
						CREATE TRIGGER prune_probe_trg BEFORE DELETE ON rls_violation_log
							FOR EACH ROW EXECUTE FUNCTION prune_probe_fn();`)
					return err
				}))
			}

			n, err := repo.PruneBefore(ctx, time.Now().Add(-24*time.Hour))

			require.Error(t, err)
			assert.Zero(t, n)
			assert.Equal(t, tc.wantUnavailable, errors.Is(err, domain.ErrUpstreamUnavailable), "classified as unavailable: %v", err)
			if tc.triggerSQLState != "" {
				var pgErr *pgconn.PgError
				require.ErrorAs(t, err, &pgErr, "the original database error stays in the chain")
				assert.Equal(t, tc.triggerSQLState, pgErr.Code)
			}
		})
	}
}
