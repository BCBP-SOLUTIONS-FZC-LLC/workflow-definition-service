//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

func w4cSetCreatedAt(t *testing.T, pool *pgcommon.Pool, id uuid.UUID, at time.Time) {
	t.Helper()
	err := pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		_, err := conn.Exec(ctx, `UPDATE workflow_version SET created_at = $2 WHERE id = $1`, id, at)
		return err
	})
	if err != nil {
		t.Fatalf("set created_at: %v", err)
	}
}

func TestWorkflowVersionRepo_Create_DraftWithoutOptionalFields_StoresNullsAndTheFlags(t *testing.T) {
	empty := ""
	cases := []struct {
		name    string
		isValid bool
		plan    *string
		errs    *string
	}{
		{"nil optional fields, valid", true, nil, nil},
		{"nil optional fields, invalid", false, nil, nil},
		{"empty-string optional fields, valid", true, &empty, &empty},
		{"empty-string optional fields, invalid", false, &empty, &empty},
	}
	pool := newTestPool(t)
	repo := postgres.NewWorkflowVersionRepo(pool)
	ctx := context.Background()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wf := w4cWorkflow(t, pool, uuid.New())
			v := newDraftVersion(wf.ID, wf.TenantID)
			v.IsValid = tc.isValid
			v.CompiledPlanJSON = tc.plan
			v.ValidationErrorsJSON = tc.errs
			v.ArtifactHash = ""
			v.VersionNumber = nil
			v.PublishedAt = nil

			if err := repo.Create(ctx, v); err != nil {
				t.Fatalf("Create: %v", err)
			}

			raw := w4cRaw(t, pool, v.ID)
			if !raw.PlanNull || !raw.HashNull || !raw.NumberNull || !raw.ErrorsNull || !raw.PublishedNull {
				t.Errorf("stored NULL flags plan=%v hash=%v number=%v errors=%v published_at=%v, want all true",
					raw.PlanNull, raw.HashNull, raw.NumberNull, raw.ErrorsNull, raw.PublishedNull)
			}
			if raw.IsValid != tc.isValid || raw.CreatedBy != v.CreatedByUserID {
				t.Errorf("stored is_valid=%v created_by=%s, want %v %s", raw.IsValid, raw.CreatedBy, tc.isValid, v.CreatedByUserID)
			}
			got := w4cStored(t, pool, wf.TenantID, v.ID)
			if got.CompiledPlanJSON != nil || got.ValidationErrorsJSON != nil || got.ArtifactHash != "" ||
				got.VersionNumber != nil || got.PublishedAt != nil {
				t.Errorf("GetByID returned optional fields %+v, want them all unset", got)
			}
			if got.IsValid != tc.isValid || got.CreatedByUserID != v.CreatedByUserID || got.Status != domain.VersionStatusDraft {
				t.Errorf("GetByID is_valid=%v created_by=%s status=%s, want %v %s DRAFT",
					got.IsValid, got.CreatedByUserID, got.Status, tc.isValid, v.CreatedByUserID)
			}
			if len(got.ModuleRefs) != 0 || raw.ModuleRefs != "[]" {
				t.Errorf("new draft has module refs %v / %s, want none", got.ModuleRefs, raw.ModuleRefs)
			}
		})
	}
}

func TestWorkflowVersionRepo_Publish_WithoutModuleRefs_StoresThePlanTheHashAndAnEmptyRefList(t *testing.T) {
	cases := []struct {
		name     string
		refs     []domain.ModuleRef
		hash     string
		wantHash bool
	}{
		{"nil module refs", nil, "hash-1", true},
		{"empty module refs", []domain.ModuleRef{}, "hash-2", true},
		{"empty hash is stored as NULL", nil, "", false},
	}
	pool := newTestPool(t)
	repo := postgres.NewWorkflowVersionRepo(pool)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wf := w4cWorkflow(t, pool, uuid.New())
			draft := newDraftVersion(wf.ID, wf.TenantID)
			if err := repo.Create(context.Background(), draft); err != nil {
				t.Fatalf("Create: %v", err)
			}
			readAt := w4cStored(t, pool, wf.TenantID, draft.ID)
			plan := `{"steps":[{"id":"a"}]}`

			if err := repo.Publish(context.Background(), wf.TenantID, draft.ID, readAt.RecordVersion, 7, plan, tc.hash, tc.refs); err != nil {
				t.Fatalf("Publish: %v", err)
			}

			raw := w4cRaw(t, pool, draft.ID)
			if raw.ModuleRefs != "[]" {
				t.Errorf("module_refs = %s, want []", raw.ModuleRefs)
			}
			if raw.PlanNull || !w4cJSONEqual(raw.Plan, plan) {
				t.Errorf("stored plan = %q (null=%v), want %s", raw.Plan, raw.PlanNull, plan)
			}
			if raw.HashNull == tc.wantHash || raw.Hash != tc.hash {
				t.Errorf("stored hash = %q (null=%v), want %q (stored=%v)", raw.Hash, raw.HashNull, tc.hash, tc.wantHash)
			}
			if raw.NumberNull || raw.PublishedNull {
				t.Errorf("version_number null=%v published_at null=%v, want both set", raw.NumberNull, raw.PublishedNull)
			}
			got := w4cStored(t, pool, wf.TenantID, draft.ID)
			if got.Status != domain.VersionStatusPublished || got.VersionNumber == nil || *got.VersionNumber != 7 {
				t.Errorf("status %s number %v, want PUBLISHED 7", got.Status, got.VersionNumber)
			}
			if got.ArtifactHash != tc.hash || len(got.ModuleRefs) != 0 {
				t.Errorf("GetByID hash %q refs %v, want %q and none", got.ArtifactHash, got.ModuleRefs, tc.hash)
			}
		})
	}
}

func TestWorkflowVersionRepo_GetByIDAndGetDraft_ReturnEveryStoredField(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowVersionRepo(pool)
	ctx := context.Background()
	wf := w4cWorkflow(t, pool, uuid.New())
	draft := newDraftVersion(wf.ID, wf.TenantID)
	draft.BPMNXML = "<definitions><process id=\"w4c\"/></definitions>"
	plan, errs := `{"steps":[{"id":"a"}]}`, `[{"code":"W4C"}]`
	draft.CompiledPlanJSON, draft.ValidationErrorsJSON = &plan, &errs
	draft.ArtifactHash, draft.IsValid = "w4c-hash", false
	if err := repo.Create(ctx, draft); err != nil {
		t.Fatalf("Create: %v", err)
	}

	check := func(t *testing.T, label string, got *domain.WorkflowVersion) {
		t.Helper()
		raw := w4cRaw(t, pool, draft.ID)
		if got.ID != draft.ID || got.WorkflowID != wf.ID || got.TenantID != wf.TenantID || got.Status != domain.VersionStatusDraft {
			t.Errorf("%s: identity/status %s %s %s %s mismatch", label, got.ID, got.WorkflowID, got.TenantID, got.Status)
		}
		if got.BPMNXML != draft.BPMNXML || got.ArtifactHash != "w4c-hash" || got.IsValid || got.CreatedByUserID != draft.CreatedByUserID {
			t.Errorf("%s: bpmn/hash/is_valid/created_by mismatch: %+v", label, got)
		}
		if got.CompiledPlanJSON == nil || !w4cJSONEqual(*got.CompiledPlanJSON, plan) {
			t.Errorf("%s: plan = %v, want %s", label, got.CompiledPlanJSON, plan)
		}
		if got.ValidationErrorsJSON == nil || !w4cJSONEqual(*got.ValidationErrorsJSON, errs) {
			t.Errorf("%s: errors = %v, want %s", label, got.ValidationErrorsJSON, errs)
		}
		if got.RecordVersion != raw.RecordVersion || got.RecordVersion < 1 {
			t.Errorf("%s: record_version = %d, stored %d", label, got.RecordVersion, raw.RecordVersion)
		}
		if got.CreatedAt.IsZero() || !got.CreatedAt.Equal(raw.CreatedAt) || got.UpdatedAt.IsZero() || !got.UpdatedAt.Equal(raw.UpdatedAt) {
			t.Errorf("%s: timestamps %v %v, stored %v %v", label, got.CreatedAt, got.UpdatedAt, raw.CreatedAt, raw.UpdatedAt)
		}
		if got.VersionNumber != nil || got.PublishedAt != nil || len(got.ModuleRefs) != 0 {
			t.Errorf("%s: a draft has number %v published_at %v refs %v", label, got.VersionNumber, got.PublishedAt, got.ModuleRefs)
		}
	}

	byID, err := repo.GetByID(ctx, wf.TenantID, draft.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	check(t, "GetByID", byID)
	byDraft, err := repo.GetDraft(ctx, wf.TenantID, wf.ID)
	if err != nil {
		t.Fatalf("GetDraft: %v", err)
	}
	check(t, "GetDraft", byDraft)
	if w4cSnapshot(t, byID) != w4cSnapshot(t, byDraft) {
		t.Errorf("GetByID and GetDraft disagree:\n %s\n %s", w4cSnapshot(t, byID), w4cSnapshot(t, byDraft))
	}

	edited := *byID
	edited.BPMNXML = "<definitions><!-- w4c edit --></definitions>"
	if err := repo.UpdateDraft(ctx, &edited); err != nil {
		t.Fatalf("UpdateDraft: %v", err)
	}
	after := w4cStored(t, pool, wf.TenantID, draft.ID)
	if after.RecordVersion != edited.RecordVersion || after.RecordVersion <= byID.RecordVersion {
		t.Errorf("record_version after an edit = %d (UpdateDraft reported %d), want above %d", after.RecordVersion, edited.RecordVersion, byID.RecordVersion)
	}
}

func TestWorkflowVersionRepo_GetDraft_SkipsPublishedAndArchivedSiblings(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowVersionRepo(pool)
	ctx := context.Background()
	wf := w4cWorkflow(t, pool, uuid.New())
	w4cVersionInStatus(t, pool, wf, domain.VersionStatusArchived, 1)
	published := w4cVersionInStatus(t, pool, wf, domain.VersionStatusPublished, 2)

	if _, err := repo.GetDraft(ctx, wf.TenantID, wf.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetDraft with no draft = %v, want ErrNotFound", err)
	}

	draft := newDraftVersion(wf.ID, wf.TenantID)
	if err := repo.Create(ctx, draft); err != nil {
		t.Fatalf("Create draft: %v", err)
	}
	got, err := repo.GetDraft(ctx, wf.TenantID, wf.ID)
	if err != nil {
		t.Fatalf("GetDraft: %v", err)
	}
	if got.ID != draft.ID || got.Status != domain.VersionStatusDraft || got.ID == published.ID {
		t.Errorf("GetDraft returned %s (%s), want the draft %s", got.ID, got.Status, draft.ID)
	}
}

func TestWorkflowVersionRepo_GetByID_PublishedVersion_ReturnsPlanHashNumberAndModuleRefs(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowVersionRepo(pool)
	ctx := context.Background()
	wf := w4cWorkflow(t, pool, uuid.New())
	draft := newDraftVersion(wf.ID, wf.TenantID)
	if err := repo.Create(ctx, draft); err != nil {
		t.Fatalf("Create: %v", err)
	}
	refs := []domain.ModuleRef{
		{ModuleID: uuid.New(), VersionID: uuid.New(), ProcessID: "m1", VersionNumber: 2},
		{ModuleID: uuid.New(), VersionID: uuid.New(), ProcessID: "m2", VersionNumber: 5},
	}
	plan := `{"steps":[{"id":"published"}]}`
	if err := repo.Publish(ctx, wf.TenantID, draft.ID, 1, 3, plan, "pub-hash", refs); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	got := w4cStored(t, pool, wf.TenantID, draft.ID)

	raw := w4cRaw(t, pool, draft.ID)
	if got.RecordVersion != raw.RecordVersion || !got.UpdatedAt.Equal(raw.UpdatedAt) {
		t.Errorf("record_version %d / updated_at %v, stored %d / %v", got.RecordVersion, got.UpdatedAt, raw.RecordVersion, raw.UpdatedAt)
	}
	if got.PublishedAt == nil || got.VersionNumber == nil || *got.VersionNumber != 3 || got.ArtifactHash != "pub-hash" {
		t.Errorf("published_at %v number %v hash %q, want set / 3 / pub-hash", got.PublishedAt, got.VersionNumber, got.ArtifactHash)
	}
	if got.CompiledPlanJSON == nil || !w4cJSONEqual(*got.CompiledPlanJSON, plan) {
		t.Errorf("plan = %v, want %s", got.CompiledPlanJSON, plan)
	}
	if len(got.ModuleRefs) != 2 || got.ModuleRefs[0] != refs[0] || got.ModuleRefs[1] != refs[1] {
		t.Errorf("module refs = %v, want %v in order", got.ModuleRefs, refs)
	}
}

func TestWorkflowVersionRepo_ListByWorkflow_ReturnsOnlyThisWorkflowsSummariesNewestFirst(t *testing.T) {
	superPool, _ := fixtures.NewTestPoolAndDSN(t)
	repo := postgres.NewWorkflowVersionRepo(superPool)
	ctx := context.Background()
	tenantID := uuid.New()
	wf := w4cWorkflow(t, superPool, tenantID)
	other := w4cWorkflow(t, superPool, tenantID)
	w4cVersionInStatus(t, superPool, other, domain.VersionStatusPublished, 1)

	refs := []domain.ModuleRef{{ModuleID: uuid.New(), VersionID: uuid.New(), ProcessID: "m1", VersionNumber: 1}}
	archived := newDraftVersion(wf.ID, tenantID)
	if err := repo.Create(ctx, archived); err != nil {
		t.Fatalf("create first: %v", err)
	}
	if err := repo.Publish(ctx, tenantID, archived.ID, 1, 1, `{"steps":["one"]}`, "hash-one", refs); err != nil {
		t.Fatalf("publish first: %v", err)
	}
	if err := repo.SetInvalid(ctx, tenantID, archived.ID, `[{"node_id":"A"}]`); err != nil {
		t.Fatalf("invalidate first: %v", err)
	}
	if err := repo.Archive(ctx, tenantID, archived.ID); err != nil {
		t.Fatalf("archive first: %v", err)
	}
	published := w4cVersionInStatus(t, superPool, wf, domain.VersionStatusPublished, 2)
	draft := newDraftVersion(wf.ID, tenantID)
	draft.BPMNXML = "<definitions><!-- large draft body --></definitions>"
	plan, errs := `{"steps":["draft"]}`, `[{"code":"D"}]`
	draft.CompiledPlanJSON, draft.ValidationErrorsJSON, draft.ArtifactHash = &plan, &errs, "draft-hash"
	if err := repo.Create(ctx, draft); err != nil {
		t.Fatalf("create draft: %v", err)
	}
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	w4cSetCreatedAt(t, superPool, archived.ID, base)
	w4cSetCreatedAt(t, superPool, published.ID, base.Add(time.Hour))
	w4cSetCreatedAt(t, superPool, draft.ID, base.Add(2*time.Hour))
	wantOrder := []uuid.UUID{draft.ID, published.ID, archived.ID}

	pages := []struct {
		page, limit int
		want        []uuid.UUID
	}{
		{1, 20, wantOrder},
		{1, 2, wantOrder[:2]},
		{2, 2, wantOrder[2:]},
		{3, 2, nil},
	}
	for _, p := range pages {
		got, total, err := repo.ListByWorkflow(ctx, tenantID, wf.ID, p.page, p.limit)
		if err != nil {
			t.Fatalf("ListByWorkflow page %d limit %d: %v", p.page, p.limit, err)
		}
		if total != 3 || len(got) != len(p.want) {
			t.Fatalf("page %d limit %d: total %d rows %d, want 3 and %d", p.page, p.limit, total, len(got), len(p.want))
		}
		for i, row := range got {
			if row.ID != p.want[i] {
				t.Errorf("page %d limit %d row %d = %s, want %s", p.page, p.limit, i, row.ID, p.want[i])
			}
		}
	}

	all, _, err := repo.ListByWorkflow(ctx, tenantID, wf.ID, 1, 20)
	if err != nil {
		t.Fatalf("ListByWorkflow: %v", err)
	}
	for _, row := range all {
		full := w4cStored(t, superPool, tenantID, row.ID)
		if row.BPMNXML != "" || row.CompiledPlanJSON != nil || row.ArtifactHash != "" || row.ValidationErrorsJSON != nil || len(row.ModuleRefs) != 0 {
			t.Errorf("summary row %s carries bpmn %q plan %v hash %q errors %v refs %v, want none of them",
				row.ID, row.BPMNXML, row.CompiledPlanJSON, row.ArtifactHash, row.ValidationErrorsJSON, row.ModuleRefs)
		}
		if row.WorkflowID != wf.ID || row.TenantID != tenantID || row.Status != full.Status || row.IsValid != full.IsValid ||
			row.CreatedByUserID != full.CreatedByUserID || row.RecordVersion != full.RecordVersion ||
			!row.CreatedAt.Equal(full.CreatedAt) || !row.UpdatedAt.Equal(full.UpdatedAt) {
			t.Errorf("summary row %+v differs from the stored version %+v", row, full)
		}
		if (row.VersionNumber == nil) != (full.VersionNumber == nil) || (row.VersionNumber != nil && *row.VersionNumber != *full.VersionNumber) {
			t.Errorf("summary row %s number %v, stored %v", row.ID, row.VersionNumber, full.VersionNumber)
		}
		if (row.PublishedAt == nil) != (full.PublishedAt == nil) {
			t.Errorf("summary row %s published_at %v, stored %v", row.ID, row.PublishedAt, full.PublishedAt)
		}
	}
	if all[2].IsValid {
		t.Errorf("the invalidated version is listed as valid")
	}
}

func TestWorkflowVersionRepo_NextVersionNumber_CountsNonDraftVersionsOfOneWorkflow(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowVersionRepo(pool)
	ctx := context.Background()
	tenantID := uuid.New()
	wf := w4cWorkflow(t, pool, tenantID)
	other := w4cWorkflow(t, pool, tenantID)

	expect := func(step string, workflowID uuid.UUID, forTenant uuid.UUID, want int32) {
		t.Helper()
		got, err := repo.NextVersionNumber(ctx, forTenant, workflowID)
		if err != nil {
			t.Fatalf("%s: NextVersionNumber: %v", step, err)
		}
		if got != want {
			t.Errorf("%s: NextVersionNumber = %d, want %d", step, got, want)
		}
	}

	expect("no versions", wf.ID, tenantID, 1)
	draft := newDraftVersion(wf.ID, tenantID)
	if err := repo.Create(ctx, draft); err != nil {
		t.Fatalf("create draft: %v", err)
	}
	expect("only a draft", wf.ID, tenantID, 1)

	if err := repo.Publish(ctx, tenantID, draft.ID, 1, 1, `{"steps":[]}`, "h1", nil); err != nil {
		t.Fatalf("publish 1: %v", err)
	}
	expect("published number 1", wf.ID, tenantID, 2)

	second := newDraftVersion(wf.ID, tenantID)
	if err := repo.Create(ctx, second); err != nil {
		t.Fatalf("create second draft: %v", err)
	}
	expect("published 1 and a draft", wf.ID, tenantID, 2)
	if err := repo.Publish(ctx, tenantID, second.ID, 1, 5, `{"steps":[]}`, "h5", nil); err != nil {
		t.Fatalf("publish 5: %v", err)
	}
	expect("published 1 and 5", wf.ID, tenantID, 6)

	if err := repo.Archive(ctx, tenantID, second.ID); err != nil {
		t.Fatalf("archive 5: %v", err)
	}
	expect("number 5 archived", wf.ID, tenantID, 6)

	expect("other workflow without versions", other.ID, tenantID, 1)
	expect("this workflow asked as another tenant", wf.ID, uuid.New(), 1)
}

func TestWorkflowVersionRepo_EveryCall_ClosedPool_ReturnsUpstreamUnavailable(t *testing.T) {
	calls := []struct {
		name string
		run  func(ctx context.Context, repo *postgres.WorkflowVersionRepo, tenantID, id uuid.UUID) error
	}{
		{"Create", func(ctx context.Context, repo *postgres.WorkflowVersionRepo, tenantID, id uuid.UUID) error {
			return repo.Create(ctx, newDraftVersion(uuid.New(), tenantID))
		}},
		{"GetByID", func(ctx context.Context, repo *postgres.WorkflowVersionRepo, tenantID, id uuid.UUID) error {
			_, err := repo.GetByID(ctx, tenantID, id)
			return err
		}},
		{"GetDraft", func(ctx context.Context, repo *postgres.WorkflowVersionRepo, tenantID, id uuid.UUID) error {
			_, err := repo.GetDraft(ctx, tenantID, id)
			return err
		}},
		{"ListByWorkflow", func(ctx context.Context, repo *postgres.WorkflowVersionRepo, tenantID, id uuid.UUID) error {
			_, _, err := repo.ListByWorkflow(ctx, tenantID, id, 1, 20)
			return err
		}},
		{"UpdateDraft", func(ctx context.Context, repo *postgres.WorkflowVersionRepo, tenantID, id uuid.UUID) error {
			v := newDraftVersion(uuid.New(), tenantID)
			v.ID, v.RecordVersion = id, 1
			return repo.UpdateDraft(ctx, v)
		}},
		{"Publish", func(ctx context.Context, repo *postgres.WorkflowVersionRepo, tenantID, id uuid.UUID) error {
			return repo.Publish(ctx, tenantID, id, 1, 1, `{"steps":[]}`, "h", nil)
		}},
		{"Archive", func(ctx context.Context, repo *postgres.WorkflowVersionRepo, tenantID, id uuid.UUID) error {
			return repo.Archive(ctx, tenantID, id)
		}},
		{"DeleteDraft", func(ctx context.Context, repo *postgres.WorkflowVersionRepo, tenantID, id uuid.UUID) error {
			return repo.DeleteDraft(ctx, tenantID, id)
		}},
		{"SetInvalid", func(ctx context.Context, repo *postgres.WorkflowVersionRepo, tenantID, id uuid.UUID) error {
			return repo.SetInvalid(ctx, tenantID, id, `[{"node_id":"A"}]`)
		}},
		{"NextVersionNumber", func(ctx context.Context, repo *postgres.WorkflowVersionRepo, tenantID, id uuid.UUID) error {
			_, err := repo.NextVersionNumber(ctx, tenantID, id)
			return err
		}},
		{"ListModuleUsages", func(ctx context.Context, repo *postgres.WorkflowVersionRepo, tenantID, id uuid.UUID) error {
			_, err := repo.ListModuleUsages(ctx, tenantID, id)
			return err
		}},
	}
	_, dsn := fixtures.NewTestPoolAndDSN(t)
	for _, c := range calls {
		t.Run(c.name, func(t *testing.T) {
			closed, err := pgcommon.NewPool(context.Background(), pgcommon.Config{DSN: dsn})
			if err != nil {
				t.Fatalf("open pool: %v", err)
			}
			closed.Close()

			err = c.run(context.Background(), postgres.NewWorkflowVersionRepo(closed), uuid.New(), uuid.New())

			if !errors.Is(err, domain.ErrUpstreamUnavailable) {
				t.Errorf("%s on a closed pool = %v, want ErrUpstreamUnavailable", c.name, err)
			}
			if errors.Is(err, domain.ErrNotFound) {
				t.Errorf("%s on a closed pool = %v, an outage must not read as not-found", c.name, err)
			}
		})
	}
}

func TestWorkflowVersionRepo_TransactionInContext_IsUsedInsteadOfThePool(t *testing.T) {
	livePool, dsn := fixtures.NewTestPoolAndDSN(t)
	closed, err := pgcommon.NewPool(context.Background(), pgcommon.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	closed.Close()
	closedRepo := postgres.NewWorkflowVersionRepo(closed)
	liveRepo := postgres.NewWorkflowVersionRepo(livePool)
	transactor := postgres.NewTransactor(livePool)
	ctx := context.Background()
	wf := w4cWorkflow(t, livePool, uuid.New())

	committed := newDraftVersion(wf.ID, wf.TenantID)
	err = transactor.RunInTx(ctx, func(txCtx context.Context) error {
		if err := closedRepo.Create(txCtx, committed); err != nil {
			return err
		}
		_, err := closedRepo.GetByID(txCtx, wf.TenantID, committed.ID)
		return err
	})
	if err != nil {
		t.Fatalf("calls through a repo whose own pool is closed, inside a transaction = %v, want success", err)
	}
	if _, err := liveRepo.GetByID(ctx, wf.TenantID, committed.ID); err != nil {
		t.Fatalf("committed version not stored: %v", err)
	}

	rolledBack := newDraftVersion(uuid.New(), wf.TenantID)
	rolledBack.WorkflowID = wf.ID
	if err := liveRepo.DeleteDraft(ctx, wf.TenantID, committed.ID); err != nil {
		t.Fatalf("clear draft: %v", err)
	}
	sentinel := errors.New("force rollback")
	err = transactor.RunInTx(ctx, func(txCtx context.Context) error {
		if err := closedRepo.Create(txCtx, rolledBack); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("RunInTx = %v, want the sentinel", err)
	}
	if _, err := liveRepo.GetByID(ctx, wf.TenantID, rolledBack.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("after rollback GetByID = %v, want ErrNotFound", err)
	}
}
