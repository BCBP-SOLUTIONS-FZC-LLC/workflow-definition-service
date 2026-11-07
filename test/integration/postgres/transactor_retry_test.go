//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

// transactorProbeSetup installs a trigger on workflow inserts. In "fail" mode the first
// `threshold` inserts (counted by a sequence, which a rollback does not undo) raise a
// serialization failure; in "isolation" mode every insert raises an error naming the
// isolation level of the transaction it ran in.
func transactorProbeSetup(t *testing.T, pool *pgcommon.Pool) {
	t.Helper()
	require.NoError(t, pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		_, err := conn.Exec(ctx, `
			CREATE SEQUENCE transactor_probe_attempts;
			CREATE TABLE transactor_probe (mode text NOT NULL, threshold int NOT NULL);
			INSERT INTO transactor_probe VALUES ('fail', 0);
			CREATE FUNCTION transactor_probe_fn() RETURNS trigger LANGUAGE plpgsql AS $$
			DECLARE cfg transactor_probe;
			BEGIN
				SELECT * INTO cfg FROM transactor_probe;
				IF cfg.mode = 'isolation' THEN
					RAISE EXCEPTION 'isolation=%', current_setting('transaction_isolation');
				END IF;
				IF nextval('transactor_probe_attempts') <= cfg.threshold THEN
					RAISE EXCEPTION 'forced serialization failure' USING ERRCODE = '40001';
				END IF;
				RETURN NEW;
			END $$;
			CREATE TRIGGER transactor_probe_trg BEFORE INSERT ON workflow
				FOR EACH ROW EXECUTE FUNCTION transactor_probe_fn();`)
		return err
	}))
}

func transactorProbeArm(t *testing.T, pool *pgcommon.Pool, mode string, failFirst int) {
	t.Helper()
	require.NoError(t, pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		if _, err := conn.Exec(ctx, `SELECT setval('transactor_probe_attempts', 1, false)`); err != nil {
			return err
		}
		_, err := conn.Exec(ctx, `UPDATE transactor_probe SET mode = $1, threshold = $2`, mode, failFirst)
		return err
	}))
}

func transactorProbeWorkflow(tenantID uuid.UUID) *domain.Workflow {
	return &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), TemplateKey: "tx-probe-" + uuid.NewString(), Name: "X",
	}
}

func TestTransactor_RunInTxWithRetry_SerializationFailures_AreRetriedUpToFiveAttempts(t *testing.T) {
	pool := newTestPool(t)
	transactorProbeSetup(t, pool)
	tr := postgres.NewTransactor(pool)
	wfRepo := postgres.NewWorkflowRepo(pool)

	cases := []struct {
		name         string
		failFirst    int
		wantAttempts int
		wantStored   bool
	}{
		{name: "no conflict runs once", failFirst: 0, wantAttempts: 1, wantStored: true},
		{name: "two conflicts then success", failFirst: 2, wantAttempts: 3, wantStored: true},
		{name: "four conflicts then success on the last attempt", failFirst: 4, wantAttempts: 5, wantStored: true},
		{name: "conflicts on every attempt exhaust the budget", failFirst: 1000, wantAttempts: 5, wantStored: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			transactorProbeArm(t, pool, "fail", tc.failFirst)
			tenantID := uuid.New()
			wf := transactorProbeWorkflow(tenantID)
			attempts := 0

			err := tr.RunInTxWithRetry(ctx, func(txCtx context.Context) error {
				attempts++
				return wfRepo.Create(txCtx, wf)
			})

			assert.Equal(t, tc.wantAttempts, attempts, "times the callback ran")
			_, getErr := wfRepo.GetByID(ctx, tenantID, wf.ID)
			if tc.wantStored {
				require.NoError(t, err)
				require.NoError(t, getErr, "the committed attempt's workflow")
				return
			}
			require.Error(t, err)
			assert.True(t, pgcommon.IsSerializationFailure(err), "the last conflict stays matchable: %v", err)
			assert.ErrorIs(t, getErr, domain.ErrNotFound, "every failed attempt rolled back")
		})
	}
}

func TestTransactor_RunInTx_SerializationFailure_IsNotRetried(t *testing.T) {
	pool := newTestPool(t)
	transactorProbeSetup(t, pool)
	transactorProbeArm(t, pool, "fail", 1)
	wfRepo := postgres.NewWorkflowRepo(pool)
	attempts := 0

	err := postgres.NewTransactor(pool).RunInTx(context.Background(), func(txCtx context.Context) error {
		attempts++
		return wfRepo.Create(txCtx, transactorProbeWorkflow(uuid.New()))
	})

	assert.Equal(t, 1, attempts)
	require.Error(t, err)
	assert.True(t, pgcommon.IsSerializationFailure(err), "the conflict is returned as it happened: %v", err)
}

func TestTransactor_IsolationLevel_PlainIsDefaultAndRetryingIsSerializable(t *testing.T) {
	pool := newTestPool(t)
	transactorProbeSetup(t, pool)
	transactorProbeArm(t, pool, "isolation", 0)
	tr := postgres.NewTransactor(pool)
	wfRepo := postgres.NewWorkflowRepo(pool)
	create := func(txCtx context.Context) error {
		return wfRepo.Create(txCtx, transactorProbeWorkflow(uuid.New()))
	}

	cases := []struct {
		name string
		run  func(context.Context, func(context.Context) error) error
		want string
	}{
		{name: "RunInTx", run: tr.RunInTx, want: "isolation=read committed"},
		{name: "RunInTxWithRetry", run: tr.RunInTxWithRetry, want: "isolation=serializable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run(context.Background(), create)

			require.Error(t, err)
			assert.ErrorContains(t, err, tc.want)
		})
	}
}

func TestTransactor_ClosedPool_IsUnavailableForBothRunners(t *testing.T) {
	cases := []struct {
		name string
		run  func(*postgres.Transactor) func(context.Context, func(context.Context) error) error
	}{
		{name: "RunInTx", run: func(tr *postgres.Transactor) func(context.Context, func(context.Context) error) error {
			return tr.RunInTx
		}},
		{name: "RunInTxWithRetry", run: func(tr *postgres.Transactor) func(context.Context, func(context.Context) error) error {
			return tr.RunInTxWithRetry
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := newTestPool(t)
			tr := postgres.NewTransactor(pool)
			pool.Close()
			called := false

			err := tc.run(tr)(context.Background(), func(context.Context) error {
				called = true
				return nil
			})

			assert.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
			assert.False(t, called, "the callback must not run without a connection")
		})
	}
}
