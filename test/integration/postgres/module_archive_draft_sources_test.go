package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

func seedWorkflow(t *testing.T, pool *pgcommon.Pool, tenantID uuid.UUID, key string) *domain.Workflow {
	t.Helper()
	wf := &domain.Workflow{ID: uuid.New(), TenantID: tenantID, CreatedByUserID: uuid.New(), TemplateKey: key, Name: key}
	if err := postgres.NewWorkflowRepo(pool).Create(context.Background(), wf); err != nil {
		t.Fatalf("create workflow %s: %v", key, err)
	}
	return wf
}

func seedDraft(t *testing.T, pool *pgcommon.Pool, wf *domain.Workflow, xml string) *domain.WorkflowVersion {
	t.Helper()
	v := newDraftVersion(wf.ID, wf.TenantID)
	v.BPMNXML = xml
	if err := postgres.NewWorkflowVersionRepo(pool).Create(context.Background(), v); err != nil {
		t.Fatalf("create draft of %s: %v", wf.TemplateKey, err)
	}
	return v
}

func publishVersion(t *testing.T, pool *pgcommon.Pool, v *domain.WorkflowVersion, number int32) {
	t.Helper()
	if err := postgres.NewWorkflowVersionRepo(pool).Publish(context.Background(), v.TenantID, v.ID, 1, number, `{}`, "h", nil); err != nil {
		t.Fatalf("publish version of %s: %v", v.WorkflowID, err)
	}
}

func TestWorkflowVersionRepo_ListDraftSources(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	repo := postgres.NewWorkflowVersionRepo(pool)

	t.Run("lists the drafts of the tenant with workflow key and BPMN, ordered by key", func(t *testing.T) {
		tenantID := uuid.New()
		wfB := seedWorkflow(t, pool, tenantID, "b-claims")
		wfA := seedWorkflow(t, pool, tenantID, "a-onboarding")
		seedDraft(t, pool, wfB, "<b-draft/>")
		seedDraft(t, pool, wfA, "<a-draft/>")

		got, err := repo.ListDraftSources(ctx, tenantID)

		if err != nil {
			t.Fatalf("ListDraftSources: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("sources = %+v, want 2", got)
		}
		want := []domain.DraftSource{
			{WorkflowID: wfA.ID, WorkflowKey: "a-onboarding", BPMNXML: "<a-draft/>"},
			{WorkflowID: wfB.ID, WorkflowKey: "b-claims", BPMNXML: "<b-draft/>"},
		}
		for i := range want {
			if *got[i] != want[i] {
				t.Errorf("sources[%d] = %+v, want %+v", i, *got[i], want[i])
			}
		}
	})

	t.Run("skips published and archived versions and keeps a draft opened after a publish", func(t *testing.T) {
		tenantID := uuid.New()
		published := seedWorkflow(t, pool, tenantID, "published-only")
		publishVersion(t, pool, seedDraft(t, pool, published, "<published/>"), 1)
		archived := seedWorkflow(t, pool, tenantID, "archived-only")
		archivedVersion := seedDraft(t, pool, archived, "<archived/>")
		publishVersion(t, pool, archivedVersion, 1)
		if err := repo.Archive(ctx, tenantID, archivedVersion.ID); err != nil {
			t.Fatalf("archive: %v", err)
		}
		reopened := seedWorkflow(t, pool, tenantID, "reopened")
		publishVersion(t, pool, seedDraft(t, pool, reopened, "<v1/>"), 1)
		seedDraft(t, pool, reopened, "<v2-draft/>")

		got, err := repo.ListDraftSources(ctx, tenantID)

		if err != nil {
			t.Fatalf("ListDraftSources: %v", err)
		}
		if len(got) != 1 || got[0].WorkflowID != reopened.ID || got[0].BPMNXML != "<v2-draft/>" {
			t.Errorf("sources = %+v, want only the reopened workflow's draft", got)
		}
	})

	t.Run("a tenant with no drafts gets none", func(t *testing.T) {
		got, err := repo.ListDraftSources(ctx, uuid.New())

		if err != nil || len(got) != 0 {
			t.Errorf("ListDraftSources = %+v, %v, want none", got, err)
		}
	})
}

func TestWorkflowVersionRepo_ListDraftSources_OtherTenantsDraftsAreHiddenByRLS(t *testing.T) {
	pool, dsn := fixtures.NewTestPoolAndDSN(t)
	ctx := context.Background()
	own, other, user := uuid.New(), uuid.New(), uuid.New()
	seedDraft(t, pool, seedWorkflow(t, pool, own, "own"), "<own/>")
	seedDraft(t, pool, seedWorkflow(t, pool, other, "other"), "<other/>")
	repo := postgres.NewWorkflowVersionRepo(fixtures.NewRestrictedRolePool(t, dsn, false, &own, &user))

	cases := []struct {
		name    string
		askedAs uuid.UUID
		wantN   int
	}{
		{name: "asked under the caller's own tenant id", askedAs: own, wantN: 1},
		{name: "asked under the other tenant's id on the caller's pool", askedAs: other, wantN: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := repo.ListDraftSources(ctx, tc.askedAs)

			if err != nil {
				t.Fatalf("ListDraftSources: %v", err)
			}
			if len(got) != tc.wantN {
				t.Errorf("sources = %+v, want %d", got, tc.wantN)
			}
			for _, s := range got {
				if s.BPMNXML == "<other/>" {
					t.Errorf("the other tenant's draft leaked: %+v", s)
				}
			}
		})
	}
}

func TestWorkflowVersionRepo_ListDraftSources_ClosedPoolIsUnavailableNotEmpty(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowVersionRepo(pool)
	pool.Close()

	got, err := repo.ListDraftSources(context.Background(), uuid.New())

	if !errors.Is(err, domain.ErrUpstreamUnavailable) {
		t.Errorf("ListDraftSources on a closed pool = %v, want ErrUpstreamUnavailable", err)
	}
	if got != nil {
		t.Errorf("sources = %+v, want none on failure", got)
	}
}
