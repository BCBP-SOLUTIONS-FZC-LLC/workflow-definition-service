//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestWorkflowService_Archive_WorkflowOfAnotherTenant_ReturnsNotFoundAndLeavesItPublished(t *testing.T) {
	cases := []struct {
		name string
	}{
		{name: "caller tenant differs from the owning tenant"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := newTestPool(t)
			wfRepo := postgres.NewWorkflowRepo(pool)
			vRepo := postgres.NewWorkflowVersionRepo(pool)
			svc := service.NewWorkflowService(service.WorkflowDeps{
				Transactor: postgres.NewTransactor(pool), Workflows: wfRepo, Versions: vRepo,
			})
			ctx := context.Background()

			owner, intruder := uuid.New(), uuid.New()
			wf := &domain.Workflow{
				ID: uuid.New(), TenantID: owner,
				CreatedByUserID: uuid.New(), TemplateKey: "cross-tenant-archive", Name: "X",
			}
			if err := wfRepo.Create(ctx, wf); err != nil {
				t.Fatalf("Create: %v", err)
			}
			v := newDraftVersion(wf.ID, owner)
			if err := vRepo.Create(ctx, v); err != nil {
				t.Fatalf("Create version: %v", err)
			}
			if err := vRepo.Publish(ctx, owner, v.ID, 1, 1, `{}`, "h", nil); err != nil {
				t.Fatalf("Publish: %v", err)
			}
			if err := wfRepo.UpdateActiveVersion(ctx, owner, wf.ID, &v.ID); err != nil {
				t.Fatalf("UpdateActiveVersion: %v", err)
			}

			err := svc.Archive(ctx, intruder, uuid.New(), wf.ID)

			if !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("Archive as another tenant = %v, want ErrNotFound", err)
			}
			got, err := wfRepo.GetByID(ctx, owner, wf.ID)
			if err != nil {
				t.Fatalf("GetByID: %v", err)
			}
			if got.ActiveVersionID == nil || *got.ActiveVersionID != v.ID {
				t.Errorf("ActiveVersionID = %v, want %v left in place", got.ActiveVersionID, v.ID)
			}
			version, err := vRepo.GetByID(ctx, owner, v.ID)
			if err != nil {
				t.Fatalf("GetByID version: %v", err)
			}
			if version.Status != domain.VersionStatusPublished {
				t.Errorf("version status = %s, want %s", version.Status, domain.VersionStatusPublished)
			}
		})
	}
}
