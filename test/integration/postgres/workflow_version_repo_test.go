package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
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
	if err := vRepo.Publish(ctx, tenantID, v.ID, 1, 1, `{}`, "hash1", nil); err != nil {
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

	if err := vRepo.Publish(ctx, tenantID, v.ID, 1, 1, `{"steps":[]}`, "abc123", nil); err != nil {
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

// PgBouncer mode runs pgx on the simple protocol, where a JSONB parameter
// typed []byte goes out as bytea hex and Postgres refuses it.
func TestWorkflowVersionRepo_Publish_PgBouncerMode(t *testing.T) {
	_, dsn := fixtures.NewTestPoolAndDSN(t)
	ctx := context.Background()
	pool, err := pgcommon.NewPool(ctx, pgcommon.Config{DSN: dsn, PGBouncerMode: true})
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	wfRepo := postgres.NewWorkflowRepo(pool)
	vRepo := postgres.NewWorkflowVersionRepo(pool)

	tenantID := uuid.New()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), BusinessKey: "wv-pub-bouncer", Name: "X",
	}
	if err := wfRepo.Create(ctx, wf); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	ref := domain.ModuleRef{ModuleID: uuid.New(), VersionID: uuid.New(), ProcessID: "m", VersionNumber: 1}
	for _, refs := range [][]domain.ModuleRef{nil, {ref}} {
		v := newDraftVersion(wf.ID, tenantID)
		if err := vRepo.Create(ctx, v); err != nil {
			t.Fatalf("create draft: %v", err)
		}
		if err := vRepo.Publish(ctx, tenantID, v.ID, 1, int32(len(refs)+1), `{"steps":[]}`, "abc123", refs); err != nil {
			t.Fatalf("Publish with %d module refs: %v", len(refs), err)
		}
	}
}

func TestWorkflowVersionRepo_Publish_DraftEditedAfterRead_ReturnsDraftConcurrency(t *testing.T) {
	pool := newTestPool(t)
	wfRepo := postgres.NewWorkflowRepo(pool)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), BusinessKey: "wv-pub-stale", Name: "X",
	}
	if err := wfRepo.Create(ctx, wf); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	draft := newDraftVersion(wf.ID, tenantID)
	if err := vRepo.Create(ctx, draft); err != nil {
		t.Fatalf("create draft: %v", err)
	}
	readAt, err := vRepo.GetByID(ctx, tenantID, draft.ID)
	if err != nil {
		t.Fatalf("read draft: %v", err)
	}

	edited := *readAt
	edited.BPMNXML = "<definitions><!-- edited --></definitions>"
	if err := vRepo.UpdateDraft(ctx, &edited); err != nil {
		t.Fatalf("edit draft: %v", err)
	}

	err = vRepo.Publish(ctx, tenantID, draft.ID, readAt.RecordVersion, 1, `{"steps":[]}`, "stale-hash", nil)
	if !errors.Is(err, domain.ErrDraftConcurrency) {
		t.Fatalf("Publish after the draft changed = %v, want ErrDraftConcurrency", err)
	}

	got, err := vRepo.GetByID(ctx, tenantID, draft.ID)
	if err != nil {
		t.Fatalf("read after refused publish: %v", err)
	}
	if got.Status != domain.VersionStatusDraft || got.BPMNXML != edited.BPMNXML {
		t.Errorf("refused publish left status %v and bpmn %q, want the edited draft", got.Status, got.BPMNXML)
	}
}

func TestWorkflowVersionRepo_UpdateDraft_UnchangedSaveKeepsRecordVersionInSync(t *testing.T) {
	pool := newTestPool(t)
	wfRepo := postgres.NewWorkflowRepo(pool)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), BusinessKey: "wv-noop-save", Name: "X",
	}
	if err := wfRepo.Create(ctx, wf); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	draft := newDraftVersion(wf.ID, tenantID)
	if err := vRepo.Create(ctx, draft); err != nil {
		t.Fatalf("create draft: %v", err)
	}
	held, err := vRepo.GetByID(ctx, tenantID, draft.ID)
	if err != nil {
		t.Fatalf("read draft: %v", err)
	}

	for save := 1; save <= 2; save++ {
		if err := vRepo.UpdateDraft(ctx, held); err != nil {
			t.Fatalf("save %d of unchanged draft: %v", save, err)
		}
		stored, err := vRepo.GetByID(ctx, tenantID, draft.ID)
		if err != nil {
			t.Fatalf("read after save %d: %v", save, err)
		}
		if held.RecordVersion != stored.RecordVersion {
			t.Fatalf("after save %d the caller holds record_version %d but the row has %d", save, held.RecordVersion, stored.RecordVersion)
		}
	}
}

func TestWorkflowVersionRepo_Publish_NotDraft(t *testing.T) {
	pool := newTestPool(t)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	ctx := context.Background()

	// Non-existent version → ErrNotFound.
	err := vRepo.Publish(ctx, uuid.New(), uuid.New(), 1, 1, `{}`, "", nil)
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

func TestWorkflowVersionRepo_SetInvalid_KeepsEveryRevocationsNodes(t *testing.T) {
	pool := newTestPool(t)
	wfRepo := postgres.NewWorkflowRepo(pool)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), BusinessKey: "wv-inv-merge", Name: "X",
	}
	if err := wfRepo.Create(ctx, wf); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	v := newDraftVersion(wf.ID, tenantID)
	if err := vRepo.Create(ctx, v); err != nil {
		t.Fatalf("Create: %v", err)
	}

	first := `[{"node_id":"A","error":"gone"},{"node_id":"B","error":"gone"}]`
	second := `[{"node_id":"C","error":"gone"},{"node_id":"A","error":"gone"}]`
	for _, in := range []string{first, second, first} {
		if err := vRepo.SetInvalid(ctx, tenantID, v.ID, in); err != nil {
			t.Fatalf("SetInvalid: %v", err)
		}
	}

	got, err := vRepo.GetByID(ctx, tenantID, v.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	want := `[{"error":"gone","node_id":"A"},{"error":"gone","node_id":"B"},{"error":"gone","node_id":"C"}]`
	if got.ValidationErrorsJSON == nil || !jsonEqual(t, *got.ValidationErrorsJSON, want) {
		t.Errorf("validation errors = %v, want %s (first occurrence first, no duplicates, none lost)", got.ValidationErrorsJSON, want)
	}
}

func jsonEqual(t *testing.T, a, b string) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal([]byte(a), &x); err != nil {
		t.Fatalf("decode %q: %v", a, err)
	}
	if err := json.Unmarshal([]byte(b), &y); err != nil {
		t.Fatalf("decode %q: %v", b, err)
	}
	return reflect.DeepEqual(x, y)
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
