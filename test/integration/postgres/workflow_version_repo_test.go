package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestWorkflowVersionRepo_CreateAndGet(t *testing.T) {
	pool := newTestPool(t)
	wfRepo := postgres.NewWorkflowRepo(pool)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), BusinessKey: "wv-create", Name: "X",
	}
	if err := wfRepo.Create(ctx, wf); err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	v := newDraftVersion(wf.ID, tenantID)
	if err := vRepo.Create(ctx, v); err != nil {
		t.Fatalf("Create version: %v", err)
	}

	got, err := vRepo.GetByID(ctx, tenantID, v.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.BPMNXML != v.BPMNXML {
		t.Errorf("BPMNXML mismatch")
	}
	if got.Status != domain.VersionStatusDraft {
		t.Errorf("Status = %v, want DRAFT", got.Status)
	}
}

func TestWorkflowVersionRepo_GetByID_NotFound(t *testing.T) {
	pool := newTestPool(t)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	ctx := context.Background()

	_, err := vRepo.GetByID(ctx, uuid.New(), uuid.New())
	if err != domain.ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestWorkflowVersionRepo_GetDraft(t *testing.T) {
	pool := newTestPool(t)
	wfRepo := postgres.NewWorkflowRepo(pool)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), BusinessKey: "wv-draft", Name: "X",
	}
	if err := wfRepo.Create(ctx, wf); err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	v := newDraftVersion(wf.ID, tenantID)
	if err := vRepo.Create(ctx, v); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := vRepo.GetDraft(ctx, tenantID, wf.ID)
	if err != nil {
		t.Fatalf("GetDraft: %v", err)
	}
	if got.ID != v.ID {
		t.Errorf("ID mismatch")
	}
}

func TestWorkflowVersionRepo_GetDraft_NotFound(t *testing.T) {
	pool := newTestPool(t)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	ctx := context.Background()

	_, err := vRepo.GetDraft(ctx, uuid.New(), uuid.New())
	if err != domain.ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestWorkflowVersionRepo_DeleteDraft(t *testing.T) {
	pool := newTestPool(t)
	wfRepo := postgres.NewWorkflowRepo(pool)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), BusinessKey: "wv-del", Name: "X",
	}
	if err := wfRepo.Create(ctx, wf); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	v := newDraftVersion(wf.ID, tenantID)
	if err := vRepo.Create(ctx, v); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := vRepo.DeleteDraft(ctx, tenantID, v.ID); err != nil {
		t.Fatalf("DeleteDraft: %v", err)
	}

	_, err := vRepo.GetByID(ctx, tenantID, v.ID)
	if err != domain.ErrNotFound {
		t.Errorf("after delete: err = %v, want ErrNotFound", err)
	}
}

func TestWorkflowVersionRepo_DeleteDraft_WrongStatus(t *testing.T) {
	pool := newTestPool(t)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	ctx := context.Background()

	err := vRepo.DeleteDraft(ctx, uuid.New(), uuid.New())
	if err != domain.ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestWorkflowVersionRepo_Archive_WrongStatus(t *testing.T) {
	pool := newTestPool(t)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	ctx := context.Background()

	err := vRepo.Archive(ctx, uuid.New(), uuid.New())
	if err != domain.ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestWorkflowVersionRepo_SetInvalid(t *testing.T) {
	pool := newTestPool(t)
	wfRepo := postgres.NewWorkflowRepo(pool)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), BusinessKey: "wv-inv", Name: "X",
	}
	if err := wfRepo.Create(ctx, wf); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	v := newDraftVersion(wf.ID, tenantID)
	if err := vRepo.Create(ctx, v); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := vRepo.SetInvalid(ctx, tenantID, v.ID, `[{"code":"CYCLE_DETECTED"}]`); err != nil {
		t.Fatalf("SetInvalid: %v", err)
	}

	got, err := vRepo.GetByID(ctx, tenantID, v.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.IsValid {
		t.Error("IsValid should be false after SetInvalid")
	}
}

func TestWorkflowVersionRepo_NextVersionNumber(t *testing.T) {
	pool := newTestPool(t)
	wfRepo := postgres.NewWorkflowRepo(pool)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), BusinessKey: "wv-nvn", Name: "X",
	}
	if err := wfRepo.Create(ctx, wf); err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	n, err := vRepo.NextVersionNumber(ctx, tenantID, wf.ID)
	if err != nil {
		t.Fatalf("NextVersionNumber: %v", err)
	}
	if n != 1 {
		t.Errorf("NextVersionNumber = %d, want 1", n)
	}
}

func TestWorkflowVersionRepo_ListByWorkflow(t *testing.T) {
	pool := newTestPool(t)
	wfRepo := postgres.NewWorkflowRepo(pool)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), BusinessKey: "wv-list", Name: "X",
	}
	if err := wfRepo.Create(ctx, wf); err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	v := newDraftVersion(wf.ID, tenantID)
	if err := vRepo.Create(ctx, v); err != nil {
		t.Fatalf("Create: %v", err)
	}

	versions, total, err := vRepo.ListByWorkflow(ctx, tenantID, wf.ID, 1, 20)
	if err != nil {
		t.Fatalf("ListByWorkflow: %v", err)
	}
	if total != 1 || len(versions) != 1 {
		t.Errorf("total=%d len=%d, want 1 each", total, len(versions))
	}
}
