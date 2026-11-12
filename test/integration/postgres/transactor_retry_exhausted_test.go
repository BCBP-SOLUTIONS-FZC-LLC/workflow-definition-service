//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestTransactor_SerializationConflicts_AreUnavailableOnlyWhenTheRetriesRunOut(t *testing.T) {
	pool := newTestPool(t)
	transactorProbeSetup(t, pool)
	tr := postgres.NewTransactor(pool)
	wfRepo := postgres.NewWorkflowRepo(pool)

	cases := []struct {
		name            string
		failFirst       int
		retrying        bool
		wantUnavailable bool
		wantSuccess     bool
	}{
		{name: "retrying, conflicts on every attempt", failFirst: 1000, retrying: true, wantUnavailable: true},
		{name: "retrying, a conflict that clears", failFirst: 2, retrying: true, wantSuccess: true},
		{name: "not retrying, one conflict is returned as it happened", failFirst: 1000, retrying: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			transactorProbeArm(t, pool, "fail", tc.failFirst)
			run := tr.RunInTx
			if tc.retrying {
				run = tr.RunInTxWithRetry
			}

			err := run(context.Background(), func(txCtx context.Context) error {
				return wfRepo.Create(txCtx, transactorProbeWorkflow(uuid.New()))
			})

			if tc.wantSuccess {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Equal(t, tc.wantUnavailable, errors.Is(err, domain.ErrUpstreamUnavailable), "unavailable: %v", err)
			assert.True(t, pgcommon.IsSerializationFailure(err), "the conflict stays matchable: %v", err)
		})
	}
}
