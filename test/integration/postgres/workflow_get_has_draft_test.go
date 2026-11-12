//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestWorkflowRepo_GetByID_ReportsWhetherADraftExists(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowRepo(pool)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	ctx := context.Background()

	tests := []struct {
		name      string
		setup     func(t *testing.T, wf *domain.Workflow)
		wantDraft bool
	}{
		{
			name:      "a workflow with no version has no draft",
			setup:     func(t *testing.T, wf *domain.Workflow) {},
			wantDraft: false,
		},
		{
			name: "a workflow with a draft has a draft",
			setup: func(t *testing.T, wf *domain.Workflow) {
				if err := vRepo.Create(ctx, newDraftVersion(wf.ID, wf.TenantID)); err != nil {
					t.Fatalf("create draft: %v", err)
				}
			},
			wantDraft: true,
		},
		{
			name: "a workflow with only an active version has no draft",
			setup: func(t *testing.T, wf *domain.Workflow) {
				activateListedWorkflow(ctx, t, pool, wf, true)
			},
			wantDraft: false,
		},
		{
			name: "a workflow with an active version and a draft on top has a draft",
			setup: func(t *testing.T, wf *domain.Workflow) {
				activateListedWorkflow(ctx, t, pool, wf, true)
				if err := vRepo.Create(ctx, newDraftVersion(wf.ID, wf.TenantID)); err != nil {
					t.Fatalf("create draft on top of the active version: %v", err)
				}
			},
			wantDraft: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tenantID := uuid.New()
			wf := createListedWorkflow(ctx, t, repo, tenantID, "key", "Name")
			tc.setup(t, wf)

			got, err := repo.GetByID(ctx, tenantID, wf.ID)
			if err != nil {
				t.Fatalf("GetByID: %v", err)
			}
			if got.HasDraft != tc.wantDraft {
				t.Errorf("HasDraft = %v, want %v", got.HasDraft, tc.wantDraft)
			}
		})
	}
}
