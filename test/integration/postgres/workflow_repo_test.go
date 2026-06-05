package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

func TestWorkflowRepo_CreateAndGet(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), BusinessKey: "tender-review",
		Name: "Tender Review", Description: "Standard review process",
	}

	if err := repo.Create(ctx, wf); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.GetByID(ctx, tenantID, wf.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.BusinessKey != wf.BusinessKey {
		t.Errorf("BusinessKey = %q, want %q", got.BusinessKey, wf.BusinessKey)
	}
	if got.Description != wf.Description {
		t.Errorf("Description = %q, want %q", got.Description, wf.Description)
	}
}

func TestWorkflowRepo_GetByID_NotFound(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowRepo(pool)
	ctx := context.Background()

	_, err := repo.GetByID(ctx, uuid.New(), uuid.New())
	if err != domain.ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestWorkflowRepo_DuplicateBusinessKey(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	base := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), BusinessKey: "dup-key", Name: "A",
	}
	if err := repo.Create(ctx, base); err != nil {
		t.Fatalf("first Create: %v", err)
	}

	dup := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), BusinessKey: "dup-key", Name: "B",
	}
	if err := repo.Create(ctx, dup); err != domain.ErrDuplicateBusinessKey {
		t.Errorf("err = %v, want ErrDuplicateBusinessKey", err)
	}
}

func TestWorkflowRepo_GetByBusinessKey(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), BusinessKey: "bkey", Name: "X",
	}
	if err := repo.Create(ctx, wf); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.GetByBusinessKey(ctx, tenantID, "bkey")
	if err != nil {
		t.Fatalf("GetByBusinessKey: %v", err)
	}
	if got.ID != wf.ID {
		t.Errorf("ID = %v, want %v", got.ID, wf.ID)
	}
}

func TestWorkflowRepo_CountByTenant(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	for i := range 3 {
		wf := &domain.Workflow{
			ID: uuid.New(), TenantID: tenantID,
			CreatedByUserID: uuid.New(),
			BusinessKey:     uuid.New().String()[:8] + "-" + string(rune('a'+i)),
			Name:            "W",
		}
		if err := repo.Create(ctx, wf); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}

	count, err := repo.CountByTenant(ctx, tenantID)
	if err != nil {
		t.Fatalf("CountByTenant: %v", err)
	}
	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}
}

func TestWorkflowRepo_List_Pagination(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	for i := range 5 {
		wf := &domain.Workflow{
			ID: uuid.New(), TenantID: tenantID,
			CreatedByUserID: uuid.New(),
			BusinessKey:     uuid.New().String()[:8] + "-" + string(rune('a'+i)),
			Name:            "W",
		}
		if err := repo.Create(ctx, wf); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}

	results, total, err := repo.List(ctx, tenantID, port.WorkflowFilter{Page: 1, Limit: 3})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 5 {
		t.Errorf("total = %d, want 5", total)
	}
	if len(results) != 3 {
		t.Errorf("len(results) = %d, want 3", len(results))
	}
}

func TestWorkflowRepo_List_BusinessKeyFilter(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	for _, key := range []string{"alpha", "beta", "gamma"} {
		wf := &domain.Workflow{
			ID: uuid.New(), TenantID: tenantID,
			CreatedByUserID: uuid.New(), BusinessKey: key, Name: key,
		}
		if err := repo.Create(ctx, wf); err != nil {
			t.Fatalf("Create %s: %v", key, err)
		}
	}

	bkey := "beta"
	results, total, err := repo.List(ctx, tenantID, port.WorkflowFilter{
		BusinessKey: &bkey, Page: 1, Limit: 10,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 1 || len(results) != 1 || results[0].BusinessKey != "beta" {
		t.Errorf("unexpected results: total=%d len=%d", total, len(results))
	}
}

func TestWorkflowRepo_List_SearchFilter(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	for _, key := range []string{"invoice-flow", "tender-review", "purchase-order"} {
		wf := &domain.Workflow{
			ID: uuid.New(), TenantID: tenantID,
			CreatedByUserID: uuid.New(), BusinessKey: key,
			Name: "Workflow " + key,
		}
		if err := repo.Create(ctx, wf); err != nil {
			t.Fatalf("Create %s: %v", key, err)
		}
	}

	q := "tender"
	results, total, err := repo.List(ctx, tenantID, port.WorkflowFilter{Search: &q, Page: 1, Limit: 10})
	if err != nil {
		t.Fatalf("List search: %v", err)
	}
	if total != 1 || len(results) != 1 {
		t.Errorf("search 'tender': total=%d len=%d, want 1", total, len(results))
	}
	if results[0].BusinessKey != "tender-review" {
		t.Errorf("wrong result: %q", results[0].BusinessKey)
	}
}

func TestWorkflowRepo_List_ArchivedFilter(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowRepo(pool)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()

	// active: has an active version pointer
	active := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), BusinessKey: "active-wf", Name: "Active",
	}
	if err := repo.Create(ctx, active); err != nil {
		t.Fatalf("create active workflow: %v", err)
	}
	v := newDraftVersion(active.ID, tenantID)
	if err := vRepo.Create(ctx, v); err != nil {
		t.Fatalf("create version: %v", err)
	}
	if err := repo.UpdateActiveVersion(ctx, tenantID, active.ID, &v.ID); err != nil {
		t.Fatalf("set active version: %v", err)
	}

	// archived: no active version pointer
	archived := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), BusinessKey: "archived-wf", Name: "Archived",
	}
	if err := repo.Create(ctx, archived); err != nil {
		t.Fatalf("create archived workflow: %v", err)
	}

	archivedTrue := true
	archivedFalse := false

	results, total, err := repo.List(ctx, tenantID, port.WorkflowFilter{Archived: &archivedTrue, Page: 1, Limit: 10})
	if err != nil {
		t.Fatalf("List archived=true: %v", err)
	}
	if total != 1 || len(results) != 1 || results[0].BusinessKey != "archived-wf" {
		t.Errorf("archived=true: total=%d len=%d key=%q", total, len(results), firstKey(results))
	}

	results, total, err = repo.List(ctx, tenantID, port.WorkflowFilter{Archived: &archivedFalse, Page: 1, Limit: 10})
	if err != nil {
		t.Fatalf("List archived=false: %v", err)
	}
	if total != 1 || len(results) != 1 || results[0].BusinessKey != "active-wf" {
		t.Errorf("archived=false: total=%d len=%d key=%q", total, len(results), firstKey(results))
	}
}

func TestWorkflowRepo_List_HasDraftFilter(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowRepo(pool)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()

	// with draft
	withDraft := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), BusinessKey: "has-draft", Name: "Has Draft",
	}
	if err := repo.Create(ctx, withDraft); err != nil {
		t.Fatalf("create withDraft: %v", err)
	}
	v := newDraftVersion(withDraft.ID, tenantID)
	if err := vRepo.Create(ctx, v); err != nil {
		t.Fatalf("create draft version: %v", err)
	}

	// without draft
	noDraft := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), BusinessKey: "no-draft", Name: "No Draft",
	}
	if err := repo.Create(ctx, noDraft); err != nil {
		t.Fatalf("create noDraft: %v", err)
	}

	trueVal := true
	falseVal := false

	results, total, err := repo.List(ctx, tenantID, port.WorkflowFilter{HasDraft: &trueVal, Page: 1, Limit: 10})
	if err != nil {
		t.Fatalf("List has_draft=true: %v", err)
	}
	if total != 1 || len(results) != 1 || results[0].BusinessKey != "has-draft" {
		t.Errorf("has_draft=true: total=%d len=%d key=%q", total, len(results), firstKey(results))
	}

	results, total, err = repo.List(ctx, tenantID, port.WorkflowFilter{HasDraft: &falseVal, Page: 1, Limit: 10})
	if err != nil {
		t.Fatalf("List has_draft=false: %v", err)
	}
	if total != 1 || len(results) != 1 || results[0].BusinessKey != "no-draft" {
		t.Errorf("has_draft=false: total=%d len=%d key=%q", total, len(results), firstKey(results))
	}
}

func TestWorkflowRepo_List_CombinedFilters(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	for _, key := range []string{"tender-abc", "tender-xyz", "invoice-001"} {
		wf := &domain.Workflow{
			ID: uuid.New(), TenantID: tenantID,
			CreatedByUserID: uuid.New(), BusinessKey: key, Name: "WF " + key,
		}
		if err := repo.Create(ctx, wf); err != nil {
			t.Fatalf("Create %s: %v", key, err)
		}
	}

	q := "tender"
	bkey := "tender-abc"
	results, total, err := repo.List(ctx, tenantID, port.WorkflowFilter{
		Search: &q, BusinessKey: &bkey, Page: 1, Limit: 10,
	})
	if err != nil {
		t.Fatalf("List combined: %v", err)
	}
	if total != 1 || len(results) != 1 || results[0].BusinessKey != "tender-abc" {
		t.Errorf("combined: total=%d len=%d key=%q", total, len(results), firstKey(results))
	}
}

func TestWorkflowRepo_UpdateActiveVersion(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), BusinessKey: "upd-av", Name: "X",
	}
	if err := repo.Create(ctx, wf); err != nil {
		t.Fatalf("Create: %v", err)
	}

	versionID := uuid.New()
	if err := repo.UpdateActiveVersion(ctx, tenantID, wf.ID, &versionID); err != nil {
		t.Fatalf("UpdateActiveVersion: %v", err)
	}

	got, err := repo.GetByID(ctx, tenantID, wf.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ActiveVersionID == nil || *got.ActiveVersionID != versionID {
		t.Errorf("ActiveVersionID = %v, want %v", got.ActiveVersionID, versionID)
	}
}

func TestWorkflowRepo_UpdateActiveVersion_NotFound(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowRepo(pool)
	ctx := context.Background()

	versionID := uuid.New()
	err := repo.UpdateActiveVersion(ctx, uuid.New(), uuid.New(), &versionID)
	if err != domain.ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func firstKey(wfs []*domain.Workflow) string {
	if len(wfs) == 0 {
		return "<empty>"
	}
	return wfs[0].BusinessKey
}
