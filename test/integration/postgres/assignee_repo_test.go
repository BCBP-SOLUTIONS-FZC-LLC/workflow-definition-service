package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestAssigneeRepo_BulkInsert(t *testing.T) {
	pool := newTestPool(t)
	wfRepo := postgres.NewWorkflowRepo(pool)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	aRepo := postgres.NewAssigneeRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), BusinessKey: "assignee-bulk-insert", Name: "X",
	}
	if err := wfRepo.Create(ctx, wf); err != nil {
		t.Fatalf("Create workflow: %v", err)
	}
	v := newDraftVersion(wf.ID, tenantID)
	if err := vRepo.Create(ctx, v); err != nil {
		t.Fatalf("Create version: %v", err)
	}

	userID := uuid.New()
	assignees := []*domain.NodeAssignee{
		{ID: uuid.New(), NodeKey: "Task_1", UserID: userID, Role: "approver"},
		{ID: uuid.New(), NodeKey: "Task_2", UserID: userID, Role: "reviewer"},
	}

	if err := aRepo.BulkInsert(ctx, tenantID, v.ID, assignees); err != nil {
		t.Fatalf("BulkInsert: %v", err)
	}

	got, err := aRepo.ListByUser(ctx, tenantID, userID)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len(got) = %d, want 2", len(got))
	}
}

func TestAssigneeRepo_BulkInsert_Empty(t *testing.T) {
	pool := newTestPool(t)
	wfRepo := postgres.NewWorkflowRepo(pool)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	aRepo := postgres.NewAssigneeRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), BusinessKey: "assignee-bulk-empty", Name: "X",
	}
	if err := wfRepo.Create(ctx, wf); err != nil {
		t.Fatalf("Create workflow: %v", err)
	}
	v := newDraftVersion(wf.ID, tenantID)
	if err := vRepo.Create(ctx, v); err != nil {
		t.Fatalf("Create version: %v", err)
	}

	if err := aRepo.BulkInsert(ctx, tenantID, v.ID, nil); err != nil {
		t.Errorf("BulkInsert(nil): %v, want nil", err)
	}
	if err := aRepo.BulkInsert(ctx, tenantID, v.ID, []*domain.NodeAssignee{}); err != nil {
		t.Errorf("BulkInsert(empty): %v, want nil", err)
	}

	got, err := aRepo.ListByUser(ctx, tenantID, uuid.New())
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len(got) = %d, want 0", len(got))
	}
}

func TestAssigneeRepo_ListByUser(t *testing.T) {
	pool := newTestPool(t)
	wfRepo := postgres.NewWorkflowRepo(pool)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	aRepo := postgres.NewAssigneeRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	userID := uuid.New()
	otherUserID := uuid.New()

	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), BusinessKey: "assignee-list-user", Name: "X",
	}
	if err := wfRepo.Create(ctx, wf); err != nil {
		t.Fatalf("Create workflow: %v", err)
	}
	v := newDraftVersion(wf.ID, tenantID)
	if err := vRepo.Create(ctx, v); err != nil {
		t.Fatalf("Create version: %v", err)
	}

	all := []*domain.NodeAssignee{
		{ID: uuid.New(), NodeKey: "Task_A", UserID: userID, Role: "approver"},
		{ID: uuid.New(), NodeKey: "Task_B", UserID: otherUserID, Role: "reviewer"},
	}
	if err := aRepo.BulkInsert(ctx, tenantID, v.ID, all); err != nil {
		t.Fatalf("BulkInsert: %v", err)
	}

	got, err := aRepo.ListByUser(ctx, tenantID, userID)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].NodeKey != "Task_A" {
		t.Errorf("NodeKey = %q, want Task_A", got[0].NodeKey)
	}
	if got[0].WorkflowVersionID != v.ID {
		t.Errorf("WorkflowVersionID = %v, want %v", got[0].WorkflowVersionID, v.ID)
	}

	none, err := aRepo.ListByUser(ctx, tenantID, uuid.New())
	if err != nil {
		t.Fatalf("ListByUser unknown user: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("unknown user: len(got) = %d, want 0", len(none))
	}
}

func TestAssigneeRepo_DeleteByVersion(t *testing.T) {
	pool := newTestPool(t)
	wfRepo := postgres.NewWorkflowRepo(pool)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	aRepo := postgres.NewAssigneeRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	userID := uuid.New()

	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), BusinessKey: "assignee-delete-version", Name: "X",
	}
	if err := wfRepo.Create(ctx, wf); err != nil {
		t.Fatalf("Create workflow: %v", err)
	}
	v := newDraftVersion(wf.ID, tenantID)
	if err := vRepo.Create(ctx, v); err != nil {
		t.Fatalf("Create version: %v", err)
	}

	assignees := []*domain.NodeAssignee{
		{ID: uuid.New(), NodeKey: "Task_1", UserID: userID, Role: "approver"},
	}
	if err := aRepo.BulkInsert(ctx, tenantID, v.ID, assignees); err != nil {
		t.Fatalf("BulkInsert: %v", err)
	}

	if err := aRepo.DeleteByVersion(ctx, tenantID, v.ID); err != nil {
		t.Fatalf("DeleteByVersion: %v", err)
	}

	got, err := aRepo.ListByUser(ctx, tenantID, userID)
	if err != nil {
		t.Fatalf("ListByUser after delete: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len(got) = %d, want 0 after DeleteByVersion", len(got))
	}
}
