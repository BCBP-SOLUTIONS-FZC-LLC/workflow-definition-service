//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

func TestWorkflowVersionRepo_GetByID_OtherTenantsVersion_ReturnsNotFound(t *testing.T) {
	cases := []struct {
		name       string
		askAsOwner bool
		pool       func(t *testing.T, superPool *pgcommon.Pool, dsn string, caller uuid.UUID) *pgcommon.Pool
	}{
		{
			name: "query filters on the caller's tenant",
			pool: func(_ *testing.T, superPool *pgcommon.Pool, _ string, _ uuid.UUID) *pgcommon.Pool {
				return superPool
			},
		},
		{
			name:       "row-level security hides the other tenant's rows even when the query names the owner",
			askAsOwner: true,
			pool: func(t *testing.T, _ *pgcommon.Pool, dsn string, caller uuid.UUID) *pgcommon.Pool {
				user := uuid.New()
				return fixtures.NewRestrictedRolePool(t, dsn, false, &caller, &user)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			superPool, dsn := fixtures.NewTestPoolAndDSN(t)
			ctx := context.Background()
			owner, caller := uuid.New(), uuid.New()

			wf := &domain.Workflow{
				ID: uuid.New(), TenantID: owner,
				CreatedByUserID: uuid.New(), TemplateKey: "wv-get-isolation", Name: "X",
			}
			if err := postgres.NewWorkflowRepo(superPool).Create(ctx, wf); err != nil {
				t.Fatalf("create workflow: %v", err)
			}
			version := newDraftVersion(wf.ID, owner)
			if err := postgres.NewWorkflowVersionRepo(superPool).Create(ctx, version); err != nil {
				t.Fatalf("create version: %v", err)
			}

			callerRepo := postgres.NewWorkflowVersionRepo(tc.pool(t, superPool, dsn, caller))
			queryTenant := caller
			if tc.askAsOwner {
				queryTenant = owner
			}
			got, err := callerRepo.GetByID(ctx, queryTenant, version.ID)
			if !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("err = %v, want ErrNotFound", err)
			}
			if got != nil {
				t.Fatalf("got %+v, want nil", got)
			}

			ownerRepo := postgres.NewWorkflowVersionRepo(superPool)
			if _, err := ownerRepo.GetByID(ctx, owner, version.ID); err != nil {
				t.Fatalf("owner read: %v", err)
			}
		})
	}
}
