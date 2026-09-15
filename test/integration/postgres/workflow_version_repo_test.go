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

func TestWorkflowVersionRepo_DeleteDraft_NotFound(t *testing.T) {
	pool := newTestPool(t)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	ctx := context.Background()

	err := vRepo.DeleteDraft(ctx, uuid.New(), uuid.New())
	if err != domain.ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestWorkflowVersionRepo_Archive_NotFound(t *testing.T) {
	pool := newTestPool(t)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	ctx := context.Background()

	err := vRepo.Archive(ctx, uuid.New(), uuid.New())
	if err != domain.ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestWorkflowVersionRepo_Archive_ExistsDraft_ReturnsVersionNotPublished(t *testing.T) {
	pool := newTestPool(t)
	wfRepo := postgres.NewWorkflowRepo(pool)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), BusinessKey: "arc-draft", Name: "X",
	}
	if err := wfRepo.Create(ctx, wf); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	v := newDraftVersion(wf.ID, tenantID)
	if err := vRepo.Create(ctx, v); err != nil {
		t.Fatalf("create draft: %v", err)
	}

	// Trying to archive a DRAFT version should return ErrVersionNotPublished.
	err := vRepo.Archive(ctx, tenantID, v.ID)
	if err != domain.ErrVersionNotPublished {
		t.Errorf("Archive(DRAFT) = %v, want ErrVersionNotPublished", err)
	}
}

func TestWorkflowVersionRepo_DeleteDraft_ExistsPublished_ReturnsVersionNotDraft(t *testing.T) {
	pool := newTestPool(t)
	wfRepo := postgres.NewWorkflowRepo(pool)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), BusinessKey: "del-pub", Name: "X",
	}
	if err := wfRepo.Create(ctx, wf); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	v := newDraftVersion(wf.ID, tenantID)
	if err := vRepo.Create(ctx, v); err != nil {
		t.Fatalf("create draft: %v", err)
	}
	if err := vRepo.Publish(ctx, tenantID, v.ID, 1, `{}`, "hash1"); err != nil {
		t.Fatalf("publish: %v", err)
	}

	// Trying to delete a PUBLISHED version as draft should return ErrVersionNotDraft.
	err := vRepo.DeleteDraft(ctx, tenantID, v.ID)
	if err != domain.ErrVersionNotDraft {
		t.Errorf("DeleteDraft(PUBLISHED) = %v, want ErrVersionNotDraft", err)
	}
}

func TestWorkflowVersionRepo_Publish(t *testing.T) {
	pool := newTestPool(t)
	wfRepo := postgres.NewWorkflowRepo(pool)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), BusinessKey: "wv-pub", Name: "X",
	}
	if err := wfRepo.Create(ctx, wf); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	v := newDraftVersion(wf.ID, tenantID)
	if err := vRepo.Create(ctx, v); err != nil {
		t.Fatalf("create draft: %v", err)
	}

	if err := vRepo.Publish(ctx, tenantID, v.ID, 1, `{"steps":[]}`, "abc123"); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	got, err := vRepo.GetByID(ctx, tenantID, v.ID)
	if err != nil {
		t.Fatalf("GetByID after publish: %v", err)
	}
	if got.Status != domain.VersionStatusPublished {
		t.Errorf("Status = %v, want PUBLISHED", got.Status)
	}
	n := int32(1)
	if got.VersionNumber == nil || *got.VersionNumber != n {
		t.Errorf("VersionNumber = %v, want 1", got.VersionNumber)
	}
	if got.PublishedAt == nil {
		t.Error("PublishedAt should not be nil after Publish")
	}
}

func TestWorkflowVersionRepo_Publish_NotDraft(t *testing.T) {
	pool := newTestPool(t)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	ctx := context.Background()

	// Non-existent version → ErrNotFound.
	err := vRepo.Publish(ctx, uuid.New(), uuid.New(), 1, `{}`, "")
	if err != domain.ErrNotFound {
		t.Errorf("Publish non-existent = %v, want ErrNotFound", err)
	}
}

func TestWorkflowVersionRepo_DraftAlreadyExists(t *testing.T) {
	pool := newTestPool(t)
	wfRepo := postgres.NewWorkflowRepo(pool)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), BusinessKey: "two-drafts", Name: "X",
	}
	if err := wfRepo.Create(ctx, wf); err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	v1 := newDraftVersion(wf.ID, tenantID)
	if err := vRepo.Create(ctx, v1); err != nil {
		t.Fatalf("create first draft: %v", err)
	}

	v2 := newDraftVersion(wf.ID, tenantID)
	if err := vRepo.Create(ctx, v2); err != domain.ErrDraftAlreadyExists {
		t.Errorf("create second draft = %v, want ErrDraftAlreadyExists", err)
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
