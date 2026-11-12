//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestWorkflowVersionRepo_Create_ForAWorkflowThatIsGone_IsNotFound(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowVersionRepo(pool)
	wfRepo := postgres.NewWorkflowRepo(pool)
	ctx := context.Background()

	cases := []struct {
		name  string
		setup func(t *testing.T) *domain.WorkflowVersion
	}{
		{
			name: "the workflow never existed",
			setup: func(t *testing.T) *domain.WorkflowVersion {
				return newDraftVersion(uuid.New(), uuid.New())
			},
		},
		{
			name: "the workflow exists only in another tenant",
			setup: func(t *testing.T) *domain.WorkflowVersion {
				other := versionWriteErrorsWorkflow(t, wfRepo, uuid.New())
				return newDraftVersion(other.ID, uuid.New())
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := tc.setup(t)

			err := repo.Create(ctx, v)

			if !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("Create = %v, want ErrNotFound", err)
			}
			if err.Error() != domain.ErrNotFound.Error() {
				t.Errorf("the 404 detail must not carry the database message, got %q", err.Error())
			}
			if _, getErr := repo.GetByID(ctx, v.TenantID, v.ID); !errors.Is(getErr, domain.ErrNotFound) {
				t.Errorf("a version was stored: %v", getErr)
			}
		})
	}
}
