package postgres_test

import (
	"context"
	"errors"
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
		CreatedByUserID: uuid.New(), TemplateKey: "tender-review",
		Name: "Tender Review", Description: "Standard review process",
	}

	if err := repo.Create(ctx, wf); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.GetByID(ctx, tenantID, wf.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.TemplateKey != wf.TemplateKey {
		t.Errorf("TemplateKey = %q, want %q", got.TemplateKey, wf.TemplateKey)
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

func TestWorkflowRepo_DuplicateTemplateKey(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	base := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), TemplateKey: "dup-key", Name: "A",
	}
	if err := repo.Create(ctx, base); err != nil {
		t.Fatalf("first Create: %v", err)
	}

	dup := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), TemplateKey: "dup-key", Name: "B",
	}
	if err := repo.Create(ctx, dup); err != domain.ErrDuplicateTemplateKey {
		t.Errorf("err = %v, want ErrDuplicateTemplateKey", err)
	}
}

func TestWorkflowRepo_GetByTemplateKey(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), TemplateKey: "bkey", Name: "X",
	}
	if err := repo.Create(ctx, wf); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.GetByTemplateKey(ctx, tenantID, "bkey")
	if err != nil {
		t.Fatalf("GetByTemplateKey: %v", err)
	}
	if got.ID != wf.ID {
		t.Errorf("ID = %v, want %v", got.ID, wf.ID)
	}
}

func TestWorkflowRepo_CountByTenant(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowRepo(pool)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	for i := range 3 {
		wf := &domain.Workflow{
			ID: uuid.New(), TenantID: tenantID,
			CreatedByUserID: uuid.New(),
			TemplateKey:     uuid.New().String()[:8] + "-" + string(rune('a'+i)),
			Name:            "W",
		}
		if err := repo.Create(ctx, wf); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
		if err := vRepo.Create(ctx, newDraftVersion(wf.ID, tenantID)); err != nil {
			t.Fatalf("Create version %d: %v", i, err)
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

func TestWorkflowRepo_CountByTenant_CountsOnlyWorkflowsWithAnActiveVersionOrADraft(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowRepo(pool)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	ctx := context.Background()
	tenantID := uuid.New()

	newWorkflow := func(key string) (*domain.Workflow, *domain.WorkflowVersion) {
		wf := &domain.Workflow{ID: uuid.New(), TenantID: tenantID, CreatedByUserID: uuid.New(), TemplateKey: key, Name: key}
		if err := repo.Create(ctx, wf); err != nil {
			t.Fatalf("create %s: %v", key, err)
		}
		v := newDraftVersion(wf.ID, tenantID)
		if err := vRepo.Create(ctx, v); err != nil {
			t.Fatalf("create version of %s: %v", key, err)
		}
		return wf, v
	}

	newWorkflow("only-a-draft")

	published, pv := newWorkflow("published")
	if err := vRepo.Publish(ctx, tenantID, pv.ID, 1, 1, `{}`, "h", nil); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := repo.UpdateActiveVersion(ctx, tenantID, published.ID, &pv.ID); err != nil {
		t.Fatalf("set active version: %v", err)
	}

	archived, av := newWorkflow("archived")
	if err := vRepo.Publish(ctx, tenantID, av.ID, 1, 1, `{}`, "h", nil); err != nil {
		t.Fatalf("publish archived: %v", err)
	}
	if err := repo.UpdateActiveVersion(ctx, tenantID, archived.ID, &av.ID); err != nil {
		t.Fatalf("set active version: %v", err)
	}
	if err := vRepo.Archive(ctx, tenantID, av.ID); err != nil {
		t.Fatalf("archive version: %v", err)
	}
	if err := repo.UpdateActiveVersion(ctx, tenantID, archived.ID, nil); err != nil {
		t.Fatalf("clear active version: %v", err)
	}

	older, ov1 := newWorkflow("archived-with-an-older-published-version")
	if err := vRepo.Publish(ctx, tenantID, ov1.ID, 1, 1, `{}`, "h", nil); err != nil {
		t.Fatalf("publish older: %v", err)
	}
	ov2 := newDraftVersion(older.ID, tenantID)
	if err := vRepo.Create(ctx, ov2); err != nil {
		t.Fatalf("create second version: %v", err)
	}
	if err := vRepo.Publish(ctx, tenantID, ov2.ID, 1, 2, `{}`, "h", nil); err != nil {
		t.Fatalf("publish second: %v", err)
	}
	if err := repo.UpdateActiveVersion(ctx, tenantID, older.ID, &ov2.ID); err != nil {
		t.Fatalf("set active version: %v", err)
	}
	if err := vRepo.Archive(ctx, tenantID, ov2.ID); err != nil {
		t.Fatalf("archive active version: %v", err)
	}
	if err := repo.UpdateActiveVersion(ctx, tenantID, older.ID, nil); err != nil {
		t.Fatalf("clear active version: %v", err)
	}

	count, err := repo.CountByTenant(ctx, tenantID)
	if err != nil {
		t.Fatalf("CountByTenant: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2 (a workflow with no active version and no draft frees its slot, whatever older versions it keeps)", count)
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
			TemplateKey:     uuid.New().String()[:8] + "-" + string(rune('a'+i)),
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

func TestWorkflowRepo_List_TemplateKeyFilter(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	for _, key := range []string{"alpha", "beta", "gamma"} {
		wf := &domain.Workflow{
			ID: uuid.New(), TenantID: tenantID,
			CreatedByUserID: uuid.New(), TemplateKey: key, Name: key,
		}
		if err := repo.Create(ctx, wf); err != nil {
			t.Fatalf("Create %s: %v", key, err)
		}
	}

	bkey := "beta"
	results, total, err := repo.List(ctx, tenantID, port.WorkflowFilter{
		TemplateKey: &bkey, Page: 1, Limit: 10,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 1 || len(results) != 1 || results[0].TemplateKey != "beta" {
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
			CreatedByUserID: uuid.New(), TemplateKey: key,
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
	if results[0].TemplateKey != "tender-review" {
		t.Errorf("wrong result: %q", results[0].TemplateKey)
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
		CreatedByUserID: uuid.New(), TemplateKey: "active-wf", Name: "Active",
	}
	if err := repo.Create(ctx, active); err != nil {
		t.Fatalf("create active workflow: %v", err)
	}
	v := newDraftVersion(active.ID, tenantID)
	if err := vRepo.Create(ctx, v); err != nil {
		t.Fatalf("create version: %v", err)
	}
	if err := vRepo.Publish(ctx, tenantID, v.ID, 1, 1, `{}`, "h", nil); err != nil {
		t.Fatalf("publish version: %v", err)
	}
	if err := repo.UpdateActiveVersion(ctx, tenantID, active.ID, &v.ID); err != nil {
		t.Fatalf("set active version: %v", err)
	}

	// archived: no active version pointer
	archived := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), TemplateKey: "archived-wf", Name: "Archived",
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
	if total != 1 || len(results) != 1 || results[0].TemplateKey != "archived-wf" {
		t.Errorf("archived=true: total=%d len=%d key=%q", total, len(results), firstKey(results))
	}

	results, total, err = repo.List(ctx, tenantID, port.WorkflowFilter{Archived: &archivedFalse, Page: 1, Limit: 10})
	if err != nil {
		t.Fatalf("List archived=false: %v", err)
	}
	if total != 1 || len(results) != 1 || results[0].TemplateKey != "active-wf" {
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
		CreatedByUserID: uuid.New(), TemplateKey: "has-draft", Name: "Has Draft",
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
		CreatedByUserID: uuid.New(), TemplateKey: "no-draft", Name: "No Draft",
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
	if total != 1 || len(results) != 1 || results[0].TemplateKey != "has-draft" {
		t.Errorf("has_draft=true: total=%d len=%d key=%q", total, len(results), firstKey(results))
	}

	results, total, err = repo.List(ctx, tenantID, port.WorkflowFilter{HasDraft: &falseVal, Page: 1, Limit: 10})
	if err != nil {
		t.Fatalf("List has_draft=false: %v", err)
	}
	if total != 1 || len(results) != 1 || results[0].TemplateKey != "no-draft" {
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
			CreatedByUserID: uuid.New(), TemplateKey: key, Name: "WF " + key,
		}
		if err := repo.Create(ctx, wf); err != nil {
			t.Fatalf("Create %s: %v", key, err)
		}
	}

	q := "tender"
	bkey := "tender-abc"
	results, total, err := repo.List(ctx, tenantID, port.WorkflowFilter{
		Search: &q, TemplateKey: &bkey, Page: 1, Limit: 10,
	})
	if err != nil {
		t.Fatalf("List combined: %v", err)
	}
	if total != 1 || len(results) != 1 || results[0].TemplateKey != "tender-abc" {
		t.Errorf("combined: total=%d len=%d key=%q", total, len(results), firstKey(results))
	}
}

func TestWorkflowRepo_UpdateActiveVersion(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowRepo(pool)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), TemplateKey: "upd-av", Name: "X",
	}
	if err := repo.Create(ctx, wf); err != nil {
		t.Fatalf("Create: %v", err)
	}

	v := newDraftVersion(wf.ID, tenantID)
	if err := vRepo.Create(ctx, v); err != nil {
		t.Fatalf("Create version: %v", err)
	}

	if err := vRepo.Publish(ctx, tenantID, v.ID, 1, 1, `{}`, "h", nil); err != nil {
		t.Fatalf("publish version: %v", err)
	}
	if err := repo.UpdateActiveVersion(ctx, tenantID, wf.ID, &v.ID); err != nil {
		t.Fatalf("UpdateActiveVersion: %v", err)
	}

	got, err := repo.GetByID(ctx, tenantID, wf.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ActiveVersionID == nil || *got.ActiveVersionID != v.ID {
		t.Errorf("ActiveVersionID = %v, want %v", got.ActiveVersionID, v.ID)
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

func TestWorkflowRepo_UpdateMetadata(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), TemplateKey: "upd-meta", Name: "Original", Description: "old desc",
	}
	if err := repo.Create(ctx, wf); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.UpdateMetadata(ctx, tenantID, wf.ID, "Updated Name", "new desc"); err != nil {
		t.Fatalf("UpdateMetadata: %v", err)
	}

	got, err := repo.GetByID(ctx, tenantID, wf.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "Updated Name" {
		t.Errorf("Name = %q, want %q", got.Name, "Updated Name")
	}
	if got.Description != "new desc" {
		t.Errorf("Description = %q, want %q", got.Description, "new desc")
	}
}

func TestWorkflowRepo_UpdateMetadata_NotFound(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowRepo(pool)
	ctx := context.Background()

	err := repo.UpdateMetadata(ctx, uuid.New(), uuid.New(), "X", "")
	if err != domain.ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func firstKey(wfs []*domain.Workflow) string {
	if len(wfs) == 0 {
		return "<empty>"
	}
	return wfs[0].TemplateKey
}

func TestWorkflowRepo_UpdateActiveVersion_OnlyAPublishedVersionOfThatWorkflow(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowRepo(pool)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	newWorkflow := func(key string) *domain.Workflow {
		wf := &domain.Workflow{ID: uuid.New(), TenantID: tenantID, CreatedByUserID: uuid.New(), TemplateKey: key, Name: key}
		if err := repo.Create(ctx, wf); err != nil {
			t.Fatalf("create workflow %s: %v", key, err)
		}
		return wf
	}
	wf, other := newWorkflow("guard-wf"), newWorkflow("guard-other")

	draft := newDraftVersion(wf.ID, tenantID)
	if err := vRepo.Create(ctx, draft); err != nil {
		t.Fatalf("create draft: %v", err)
	}
	if err := repo.UpdateActiveVersion(ctx, tenantID, wf.ID, &draft.ID); !errors.Is(err, domain.ErrVersionNotPublished) {
		t.Errorf("activating a draft = %v, want ErrVersionNotPublished", err)
	}

	foreign := newDraftVersion(other.ID, tenantID)
	if err := vRepo.Create(ctx, foreign); err != nil {
		t.Fatalf("create foreign draft: %v", err)
	}
	if err := vRepo.Publish(ctx, tenantID, foreign.ID, 1, 1, `{}`, "h", nil); err != nil {
		t.Fatalf("publish foreign: %v", err)
	}
	if err := repo.UpdateActiveVersion(ctx, tenantID, wf.ID, &foreign.ID); !errors.Is(err, domain.ErrVersionNotPublished) {
		t.Errorf("activating another workflow's version = %v, want ErrVersionNotPublished", err)
	}

	if err := repo.UpdateActiveVersion(ctx, tenantID, wf.ID, nil); err != nil {
		t.Errorf("clearing the pointer = %v, want nil", err)
	}
}

func TestWorkflowRepo_UpdateActiveVersion_RefusesARevokedVersion(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowRepo(pool)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), TemplateKey: "upd-av-invalid", Name: "X",
	}
	if err := repo.Create(ctx, wf); err != nil {
		t.Fatalf("Create: %v", err)
	}
	v := newDraftVersion(wf.ID, tenantID)
	if err := vRepo.Create(ctx, v); err != nil {
		t.Fatalf("Create version: %v", err)
	}
	if err := vRepo.Publish(ctx, tenantID, v.ID, 1, 1, `{}`, "h", nil); err != nil {
		t.Fatalf("publish version: %v", err)
	}
	if err := vRepo.SetInvalid(ctx, tenantID, v.ID, `[{"node_id":"A","error":"gone"}]`); err != nil {
		t.Fatalf("SetInvalid: %v", err)
	}

	if err := repo.UpdateActiveVersion(ctx, tenantID, wf.ID, &v.ID); err != domain.ErrInvalidVersionStatus {
		t.Errorf("UpdateActiveVersion of a revoked version = %v, want ErrInvalidVersionStatus", err)
	}
	got, err := repo.GetByID(ctx, tenantID, wf.ID)
	if err != nil || got.ActiveVersionID != nil {
		t.Errorf("active version = %v, %v; want none", got.ActiveVersionID, err)
	}
}

func TestWorkflowRepo_AClosedPoolIsUnavailableNotAnInternalError(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowRepo(pool)
	pool.Close()

	_, err := repo.GetByID(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, domain.ErrUpstreamUnavailable) {
		t.Errorf("GetByID on a closed pool = %v, want ErrUpstreamUnavailable so the API answers 503 and the client retries", err)
	}
}
