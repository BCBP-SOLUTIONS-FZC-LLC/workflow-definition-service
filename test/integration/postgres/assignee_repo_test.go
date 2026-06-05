package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestAssigneeRepo_BulkInsertAndList(t *testing.T) {
	pool := newTestPool(t)
	wfRepo := postgres.NewWorkflowRepo(pool)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	aRepo := postgres.NewAssigneeRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), BusinessKey: "ar-bulk", Name: "X",
	}
	if err := wfRepo.Create(ctx, wf); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	v := newDraftVersion(wf.ID, tenantID)
	if err := vRepo.Create(ctx, v); err != nil {
		t.Fatalf("create version: %v", err)
	}

	userID := uuid.New()
	assignees := []*domain.NodeAssignee{
		{
			ID: uuid.New(), TenantID: tenantID, WorkflowVersionID: v.ID,
			NodeKey: "task-1", UserID: userID, DepartmentID: "engineering", Role: "reviewer",
		},
		{
			ID: uuid.New(), TenantID: tenantID, WorkflowVersionID: v.ID,
			NodeKey: "task-2", UserID: userID, DepartmentID: "engineering", Role: "approver",
		},
	}

	if err := aRepo.BulkInsert(ctx, tenantID, v.ID, assignees); err != nil {
		t.Fatalf("BulkInsert: %v", err)
	}

	results, err := aRepo.ListByUser(ctx, tenantID, userID)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("len = %d, want 2", len(results))
	}
}

func TestAssigneeRepo_BulkInsert_Empty(t *testing.T) {
	pool := newTestPool(t)
	aRepo := postgres.NewAssigneeRepo(pool)
	ctx := context.Background()

	if err := aRepo.BulkInsert(ctx, uuid.New(), uuid.New(), nil); err != nil {
		t.Errorf("BulkInsert nil: %v", err)
	}
}

func TestAssigneeRepo_DeleteByVersion(t *testing.T) {
	pool := newTestPool(t)
	wfRepo := postgres.NewWorkflowRepo(pool)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	aRepo := postgres.NewAssigneeRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), BusinessKey: "ar-del", Name: "X",
	}
	if err := wfRepo.Create(ctx, wf); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	v := newDraftVersion(wf.ID, tenantID)
	if err := vRepo.Create(ctx, v); err != nil {
		t.Fatalf("create version: %v", err)
	}

	userID := uuid.New()
	if err := aRepo.BulkInsert(ctx, tenantID, v.ID, []*domain.NodeAssignee{
		{
			ID: uuid.New(), TenantID: tenantID, WorkflowVersionID: v.ID,
			NodeKey: "task-1", UserID: userID, DepartmentID: "eng", Role: "reviewer",
		},
	}); err != nil {
		t.Fatalf("BulkInsert: %v", err)
	}

	if err := aRepo.DeleteByVersion(ctx, tenantID, v.ID); err != nil {
		t.Fatalf("DeleteByVersion: %v", err)
	}

	results, err := aRepo.ListByUser(ctx, tenantID, userID)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("after delete: len = %d, want 0", len(results))
	}
}
