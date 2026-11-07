//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func seedDraftUpdateWorkflow(t *testing.T, pool *pgcommon.Pool, tenantID uuid.UUID, name string) (*domain.Workflow, *domain.WorkflowVersion) {
	t.Helper()
	ctx := context.Background()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), TemplateKey: "draft-update-" + uuid.NewString(), Name: name,
	}
	if err := postgres.NewWorkflowRepo(pool).Create(ctx, wf); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	draft := newDraftVersion(wf.ID, tenantID)
	if err := postgres.NewWorkflowVersionRepo(pool).Create(ctx, draft); err != nil {
		t.Fatalf("create draft: %v", err)
	}
	held, err := postgres.NewWorkflowVersionRepo(pool).GetByID(ctx, tenantID, draft.ID)
	if err != nil {
		t.Fatalf("read draft: %v", err)
	}
	return wf, held
}

func TestWorkflowVersionRepo_UpdateDraft_RowNoLongerUpdatable_ReturnsTheReasonItIsNot(t *testing.T) {
	cases := []struct {
		name    string
		change  func(t *testing.T, repo *postgres.WorkflowVersionRepo, held *domain.WorkflowVersion)
		wantErr error
	}{
		{
			name: "published after the read",
			change: func(t *testing.T, repo *postgres.WorkflowVersionRepo, held *domain.WorkflowVersion) {
				err := repo.Publish(context.Background(), held.TenantID, held.ID, held.RecordVersion, 1, `{"steps":[]}`, "hash", nil)
				if err != nil {
					t.Fatalf("publish: %v", err)
				}
			},
			wantErr: domain.ErrVersionNotDraft,
		},
		{
			name: "archived after the read",
			change: func(t *testing.T, repo *postgres.WorkflowVersionRepo, held *domain.WorkflowVersion) {
				ctx := context.Background()
				if err := repo.Publish(ctx, held.TenantID, held.ID, held.RecordVersion, 1, `{"steps":[]}`, "hash", nil); err != nil {
					t.Fatalf("publish: %v", err)
				}
				if err := repo.Archive(ctx, held.TenantID, held.ID); err != nil {
					t.Fatalf("archive: %v", err)
				}
			},
			wantErr: domain.ErrVersionNotDraft,
		},
		{
			name: "deleted after the read",
			change: func(t *testing.T, repo *postgres.WorkflowVersionRepo, held *domain.WorkflowVersion) {
				if err := repo.DeleteDraft(context.Background(), held.TenantID, held.ID); err != nil {
					t.Fatalf("delete draft: %v", err)
				}
			},
			wantErr: domain.ErrNotFound,
		},
		{
			name: "edited by someone else after the read",
			change: func(t *testing.T, repo *postgres.WorkflowVersionRepo, held *domain.WorkflowVersion) {
				other := *held
				other.BPMNXML = "<definitions><!-- other editor --></definitions>"
				if err := repo.UpdateDraft(context.Background(), &other); err != nil {
					t.Fatalf("competing edit: %v", err)
				}
			},
			wantErr: domain.ErrDraftConcurrency,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := newTestPool(t)
			repo := postgres.NewWorkflowVersionRepo(pool)
			ctx := context.Background()
			tenantID := uuid.New()
			_, held := seedDraftUpdateWorkflow(t, pool, tenantID, "X")

			tc.change(t, repo, held)
			before, beforeErr := repo.GetByID(ctx, tenantID, held.ID)

			stale := *held
			stale.BPMNXML = "<definitions><!-- stale save --></definitions>"
			err := repo.UpdateDraft(ctx, &stale)

			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("UpdateDraft = %v, want %v", err, tc.wantErr)
			}
			if beforeErr != nil {
				return
			}
			after, err := repo.GetByID(ctx, tenantID, held.ID)
			if err != nil {
				t.Fatalf("read after refused save: %v", err)
			}
			if after.BPMNXML != before.BPMNXML || after.RecordVersion != before.RecordVersion || after.Status != before.Status {
				t.Errorf("refused save changed the row: before %+v after %+v", before, after)
			}
		})
	}
}

func TestWorkflowVersionRepo_UpdateDraft_EmptyArtifacts_AreStoredAsNull(t *testing.T) {
	plan := `{"steps":[]}`
	problems := `[{"code":"E1"}]`
	empty := ""
	cases := []struct {
		name     string
		plan     *string
		problems *string
	}{
		{name: "nil pointers", plan: nil, problems: nil},
		{name: "empty strings", plan: &empty, problems: &empty},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := newTestPool(t)
			repo := postgres.NewWorkflowVersionRepo(pool)
			ctx := context.Background()
			tenantID := uuid.New()
			wf := &domain.Workflow{
				ID: uuid.New(), TenantID: tenantID,
				CreatedByUserID: uuid.New(), TemplateKey: "draft-null-" + uuid.NewString(), Name: "X",
			}
			if err := postgres.NewWorkflowRepo(pool).Create(ctx, wf); err != nil {
				t.Fatalf("create workflow: %v", err)
			}
			draft := newDraftVersion(wf.ID, tenantID)
			draft.CompiledPlanJSON = &plan
			draft.ValidationErrorsJSON = &problems
			if err := repo.Create(ctx, draft); err != nil {
				t.Fatalf("create draft: %v", err)
			}
			held, err := repo.GetByID(ctx, tenantID, draft.ID)
			if err != nil {
				t.Fatalf("read draft: %v", err)
			}
			if held.CompiledPlanJSON == nil || held.ValidationErrorsJSON == nil {
				t.Fatalf("seeded draft lost its artifacts: %+v", held)
			}

			held.CompiledPlanJSON = tc.plan
			held.ValidationErrorsJSON = tc.problems
			if err := repo.UpdateDraft(ctx, held); err != nil {
				t.Fatalf("UpdateDraft: %v", err)
			}

			var planIsNull, problemsIsNull bool
			err = pool.WithConn(ctx, func(ctx context.Context, conn *pgxpool.Conn) error {
				return conn.QueryRow(ctx,
					`SELECT compiled_plan_json IS NULL, validation_errors_json IS NULL
					   FROM workflow_version WHERE id = $1`, draft.ID).Scan(&planIsNull, &problemsIsNull)
			})
			if err != nil {
				t.Fatalf("read columns: %v", err)
			}
			if !planIsNull || !problemsIsNull {
				t.Errorf("compiled_plan_json NULL = %v, validation_errors_json NULL = %v, want both NULL", planIsNull, problemsIsNull)
			}
		})
	}
}

type draftUpdateRacingVersions struct {
	port.WorkflowVersionRepository
	rival func(ctx context.Context)
}

func (r *draftUpdateRacingVersions) UpdateDraft(ctx context.Context, v *domain.WorkflowVersion) error {
	r.rival(context.Background())
	return r.WorkflowVersionRepository.UpdateDraft(ctx, v)
}

func TestDraftService_Update_LosingTheDraftWriteRollsBackTheNameChange(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	tenantID := uuid.New()
	wf, held := seedDraftUpdateWorkflow(t, pool, tenantID, "before")

	versions := postgres.NewWorkflowVersionRepo(pool)
	workflows := postgres.NewWorkflowRepo(pool)
	rivalVersion := int64(0)
	racing := &draftUpdateRacingVersions{
		WorkflowVersionRepository: versions,
		rival: func(ctx context.Context) {
			other := *held
			other.BPMNXML = "<definitions><!-- rival --></definitions>"
			if err := versions.UpdateDraft(ctx, &other); err != nil {
				t.Errorf("rival edit: %v", err)
			}
			rivalVersion = other.RecordVersion
		},
	}
	svc := service.NewDraftService(service.DraftDeps{
		Transactor: postgres.NewTransactor(pool),
		Workflows:  workflows,
		Versions:   racing,
	})

	newName, newXML := "after", "<definitions><!-- mine --></definitions>"
	_, err := svc.Update(ctx, tenantID, uuid.New(), wf.ID, service.UpdateDraftReq{
		Name: &newName, BPMNXML: &newXML, RecordVersion: held.RecordVersion,
	})

	var conflict *domain.DraftConcurrencyError
	if !errors.As(err, &conflict) {
		t.Fatalf("Update = %v, want a draft concurrency conflict", err)
	}
	if conflict.Current != rivalVersion {
		t.Errorf("conflict carries record_version %d, want the rival's %d", conflict.Current, rivalVersion)
	}
	got, err := workflows.GetByID(ctx, tenantID, wf.ID)
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}
	if got.Name != "before" {
		t.Errorf("workflow name = %q after the lost race, want it rolled back to %q", got.Name, "before")
	}
	draft, err := versions.GetDraft(ctx, tenantID, wf.ID)
	if err != nil {
		t.Fatalf("read draft: %v", err)
	}
	if draft.BPMNXML != "<definitions><!-- rival --></definitions>" {
		t.Errorf("draft bpmn = %q, want the rival's edit to stand", draft.BPMNXML)
	}
}
