//go:build integration

package postgres_test

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

func createListedWorkflow(
	ctx context.Context, t *testing.T, repo *postgres.WorkflowRepo, tenantID uuid.UUID, key, name string,
) *domain.Workflow {
	t.Helper()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID, CreatedByUserID: uuid.New(), TemplateKey: key, Name: name,
	}
	if err := repo.Create(ctx, wf); err != nil {
		t.Fatalf("create workflow %s: %v", key, err)
	}
	return wf
}

func activateListedWorkflow(
	ctx context.Context, t *testing.T, pool *pgcommon.Pool, wf *domain.Workflow, isValid bool,
) {
	t.Helper()
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	v := newDraftVersion(wf.ID, wf.TenantID)
	if err := vRepo.Create(ctx, v); err != nil {
		t.Fatalf("create version for %s: %v", wf.TemplateKey, err)
	}
	if err := vRepo.Publish(ctx, wf.TenantID, v.ID, 1, 1, `{}`, "h", nil); err != nil {
		t.Fatalf("publish version for %s: %v", wf.TemplateKey, err)
	}
	if err := postgres.NewWorkflowRepo(pool).UpdateActiveVersion(ctx, wf.TenantID, wf.ID, &v.ID); err != nil {
		t.Fatalf("activate version for %s: %v", wf.TemplateKey, err)
	}
	if !isValid {
		if err := vRepo.SetInvalid(ctx, wf.TenantID, v.ID, `[]`); err != nil {
			t.Fatalf("mark version of %s invalid: %v", wf.TemplateKey, err)
		}
	}
}

func listedKeys(wfs []*domain.Workflow) []string {
	keys := make([]string, 0, len(wfs))
	for _, w := range wfs {
		keys = append(keys, w.TemplateKey)
	}
	slices.Sort(keys)
	return keys
}

func TestWorkflowRepo_List_ReportsTheActiveVersionAndDraftOfEachWorkflow(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowRepo(pool)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	ctx := context.Background()
	tenantID := uuid.New()

	both := createListedWorkflow(ctx, t, repo, tenantID, "both", "Both")
	activateListedWorkflow(ctx, t, pool, both, true)
	if err := vRepo.Create(ctx, newDraftVersion(both.ID, tenantID)); err != nil {
		t.Fatalf("create draft on top of the active version: %v", err)
	}
	createListedWorkflow(ctx, t, repo, tenantID, "bare", "Bare")

	results, total, err := repo.List(ctx, tenantID, port.WorkflowFilter{Page: 1, Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 2 || len(results) != 2 {
		t.Fatalf("total=%d len=%d, want 2", total, len(results))
	}

	cases := []struct {
		key          string
		wantActive   bool
		wantNumber   int32
		wantHasDraft bool
	}{
		{key: "both", wantActive: true, wantNumber: 1, wantHasDraft: true},
		{key: "bare"},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			var got *domain.Workflow
			for _, r := range results {
				if r.TemplateKey == tc.key {
					got = r
				}
			}
			if got == nil {
				t.Fatalf("workflow %q missing from the list", tc.key)
			}
			if got.HasDraft != tc.wantHasDraft {
				t.Errorf("HasDraft = %v, want %v", got.HasDraft, tc.wantHasDraft)
			}
			if tc.wantActive {
				if got.ActiveVersionID == nil || got.ActiveVersionNumber == nil {
					t.Fatalf("active version = %v / %v, want both set", got.ActiveVersionID, got.ActiveVersionNumber)
				}
				if *got.ActiveVersionNumber != tc.wantNumber {
					t.Errorf("ActiveVersionNumber = %d, want %d", *got.ActiveVersionNumber, tc.wantNumber)
				}
				return
			}
			if got.ActiveVersionID != nil || got.ActiveVersionNumber != nil {
				t.Errorf("active version = %v / %v, want none", got.ActiveVersionID, got.ActiveVersionNumber)
			}
		})
	}
}

func TestWorkflowRepo_List_IsValidFilter_MatchesTheActiveVersionOnly(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowRepo(pool)
	ctx := context.Background()
	tenantID := uuid.New()

	valid := createListedWorkflow(ctx, t, repo, tenantID, "valid", "Valid")
	activateListedWorkflow(ctx, t, pool, valid, true)
	invalid := createListedWorkflow(ctx, t, repo, tenantID, "invalid", "Invalid")
	activateListedWorkflow(ctx, t, pool, invalid, false)
	createListedWorkflow(ctx, t, repo, tenantID, "never-published", "Never published")

	yes, no := true, false
	cases := []struct {
		name    string
		isValid *bool
		want    []string
	}{
		{"is_valid false keeps only invalid active versions", &no, []string{"invalid"}},
		{"is_valid true keeps only valid active versions", &yes, []string{"valid"}},
		{"no is_valid filter keeps everything", nil, []string{"invalid", "never-published", "valid"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			results, total, err := repo.List(ctx, tenantID, port.WorkflowFilter{
				IsValid: tc.isValid, Page: 1, Limit: 10,
			})
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if got := listedKeys(results); !slices.Equal(got, tc.want) || total != int64(len(tc.want)) {
				t.Errorf("keys=%v total=%d, want %v", got, total, tc.want)
			}
		})
	}
}

func TestWorkflowRepo_List_SearchTerms_AreBoundAsData(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowRepo(pool)
	ctx := context.Background()
	tenantID := uuid.New()

	createListedWorkflow(ctx, t, repo, tenantID, "spaced", "Purchase order")
	createListedWorkflow(ctx, t, repo, tenantID, "solid", "Invoice")
	createListedWorkflow(ctx, t, repo, tenantID, "dotted", "Tender.review")

	cases := []struct {
		name   string
		search string
		want   []string
	}{
		{"a single space matches only names containing a space", " ", []string{"spaced"}},
		{"an injection attempt is literal text and matches nothing", "' OR 1=1 --", []string{}},
		{"a quote and semicolon are literal text", "'; DROP TABLE workflow; --", []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			search := tc.search
			results, total, err := repo.List(ctx, tenantID, port.WorkflowFilter{
				Search: &search, TemplateKey: nil, Page: 1, Limit: 10,
			})
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if got := listedKeys(results); !slices.Equal(got, tc.want) || total != int64(len(tc.want)) {
				t.Errorf("keys=%v total=%d, want %v", got, total, tc.want)
			}
		})
	}

	key := "' OR 1=1 --"
	results, total, err := repo.List(ctx, tenantID, port.WorkflowFilter{TemplateKey: &key, Page: 1, Limit: 10})
	if err != nil {
		t.Fatalf("List by injected key: %v", err)
	}
	if len(results) != 0 || total != 0 {
		t.Errorf("injected key matched total=%d len=%d, want none", total, len(results))
	}
	results, total, err = repo.List(ctx, tenantID, port.WorkflowFilter{Page: 1, Limit: 10})
	if err != nil || total != 3 || len(results) != 3 {
		t.Errorf("unfiltered list after the attempts = total %d, len %d, err %v; want all 3 workflows intact", total, len(results), err)
	}
}

func TestWorkflowRepo_List_ScopesRowsAndTotalToTheTenant(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowRepo(pool)
	ctx := context.Background()
	tenantA, tenantB := uuid.New(), uuid.New()

	for _, key := range []string{"a-one", "a-two"} {
		createListedWorkflow(ctx, t, repo, tenantA, key, key)
	}
	for _, key := range []string{"b-one", "b-two", "b-three"} {
		createListedWorkflow(ctx, t, repo, tenantB, key, key)
	}

	cases := []struct {
		name     string
		tenantID uuid.UUID
		want     []string
	}{
		{"tenant A sees only its own workflows", tenantA, []string{"a-one", "a-two"}},
		{"tenant B sees only its own workflows", tenantB, []string{"b-one", "b-three", "b-two"}},
		{"an unknown tenant sees nothing", uuid.New(), []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			results, total, err := repo.List(ctx, tc.tenantID, port.WorkflowFilter{Page: 1, Limit: 10})
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if got := listedKeys(results); !slices.Equal(got, tc.want) || total != int64(len(tc.want)) {
				t.Errorf("keys=%v total=%d, want %v", got, total, tc.want)
			}
		})
	}
}
