//go:build integration

package postgres_test

import (
	"bytes"
	"context"
	"encoding/json"
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

func w4cWorkflow(t *testing.T, pool *pgcommon.Pool, tenantID uuid.UUID) *domain.Workflow {
	t.Helper()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), TemplateKey: "w4c-" + uuid.NewString(), Name: "X",
	}
	if err := postgres.NewWorkflowRepo(pool).Create(context.Background(), wf); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	return wf
}

func w4cVersionInStatus(
	t *testing.T, pool *pgcommon.Pool, wf *domain.Workflow, status domain.VersionStatus, number int32,
) *domain.WorkflowVersion {
	t.Helper()
	ctx := context.Background()
	repo := postgres.NewWorkflowVersionRepo(pool)
	v := newDraftVersion(wf.ID, wf.TenantID)
	if err := repo.Create(ctx, v); err != nil {
		t.Fatalf("create draft: %v", err)
	}
	if status != domain.VersionStatusDraft {
		if err := repo.Publish(ctx, wf.TenantID, v.ID, 1, number, `{"steps":["w4c"]}`, "w4c-hash", nil); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	if status == domain.VersionStatusArchived {
		if err := repo.Archive(ctx, wf.TenantID, v.ID); err != nil {
			t.Fatalf("archive: %v", err)
		}
	}
	return w4cStored(t, pool, wf.TenantID, v.ID)
}

func w4cStored(t *testing.T, pool *pgcommon.Pool, tenantID, id uuid.UUID) *domain.WorkflowVersion {
	t.Helper()
	got, err := postgres.NewWorkflowVersionRepo(pool).GetByID(context.Background(), tenantID, id)
	if err != nil {
		t.Fatalf("read version %s: %v", id, err)
	}
	return got
}

func w4cSnapshot(t *testing.T, v *domain.WorkflowVersion) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode version: %v", err)
	}
	return string(b)
}

func w4cJSONEqual(a, b string) bool {
	var x, y any
	if json.Unmarshal([]byte(a), &x) != nil || json.Unmarshal([]byte(b), &y) != nil {
		return false
	}
	xb, _ := json.Marshal(x)
	yb, _ := json.Marshal(y)
	return bytes.Equal(xb, yb)
}

type w4cRawVersion struct {
	PlanNull, HashNull, NumberNull, ErrorsNull, PublishedNull bool

	ModuleRefs    string
	Plan          string
	Hash          string
	RecordVersion int64
	CreatedAt     time.Time
	UpdatedAt     time.Time
	IsValid       bool
	CreatedBy     uuid.UUID
}

func w4cRaw(t *testing.T, pool *pgcommon.Pool, id uuid.UUID) w4cRawVersion {
	t.Helper()
	var r w4cRawVersion
	err := pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		return conn.QueryRow(ctx, `
			SELECT compiled_plan_json IS NULL, artifact_hash IS NULL, version_number IS NULL,
			       validation_errors_json IS NULL, published_at IS NULL, module_refs::text,
			       COALESCE(compiled_plan_json::text, ''), COALESCE(artifact_hash, ''),
			       record_version, created_at, updated_at, is_valid, created_by_user_id
			FROM workflow_version WHERE id = $1`, id).Scan(
			&r.PlanNull, &r.HashNull, &r.NumberNull, &r.ErrorsNull, &r.PublishedNull, &r.ModuleRefs,
			&r.Plan, &r.Hash, &r.RecordVersion, &r.CreatedAt, &r.UpdatedAt, &r.IsValid, &r.CreatedBy)
	})
	if err != nil {
		t.Fatalf("read raw version row: %v", err)
	}
	return r
}

func TestWorkflowVersionRepo_UpdateDraftAndPublish_NonDraftVersion_ReturnsVersionNotDraftAndKeepsTheRow(t *testing.T) {
	type attempt func(ctx context.Context, repo *postgres.WorkflowVersionRepo, stored *domain.WorkflowVersion, recordVersion int64) error
	attempts := []struct {
		name string
		run  attempt
	}{
		{
			name: "UpdateDraft",
			run: func(ctx context.Context, repo *postgres.WorkflowVersionRepo, stored *domain.WorkflowVersion, rv int64) error {
				edited := *stored
				plan := `{"steps":["edited"]}`
				edited.BPMNXML = "<definitions><!-- edited --></definitions>"
				edited.CompiledPlanJSON = &plan
				edited.ArtifactHash = "edited-hash"
				edited.IsValid = false
				edited.RecordVersion = rv
				return repo.UpdateDraft(ctx, &edited)
			},
		},
		{
			name: "Publish",
			run: func(ctx context.Context, repo *postgres.WorkflowVersionRepo, stored *domain.WorkflowVersion, rv int64) error {
				return repo.Publish(ctx, stored.TenantID, stored.ID, rv, 99, `{"steps":["again"]}`, "again-hash", nil)
			},
		},
	}
	statuses := []domain.VersionStatus{domain.VersionStatusPublished, domain.VersionStatusArchived}
	pool := newTestPool(t)
	repo := postgres.NewWorkflowVersionRepo(pool)
	for _, at := range attempts {
		for _, status := range statuses {
			for _, stale := range []bool{false, true} {
				name := at.name + "/" + string(status) + "/matching record version"
				if stale {
					name = at.name + "/" + string(status) + "/stale record version"
				}
				t.Run(name, func(t *testing.T) {
					wf := w4cWorkflow(t, pool, uuid.New())
					stored := w4cVersionInStatus(t, pool, wf, status, 1)
					before := w4cSnapshot(t, stored)
					recordVersion := stored.RecordVersion
					if stale {
						recordVersion += 100
					}

					err := at.run(context.Background(), repo, stored, recordVersion)

					if !errors.Is(err, domain.ErrVersionNotDraft) {
						t.Fatalf("%s on a %s version = %v, want ErrVersionNotDraft", at.name, status, err)
					}
					if errors.Is(err, domain.ErrDraftConcurrency) {
						t.Fatalf("%s on a %s version = %v, the status must win over the stale record version", at.name, status, err)
					}
					if after := w4cSnapshot(t, w4cStored(t, pool, wf.TenantID, stored.ID)); after != before {
						t.Errorf("refused %s changed the row:\n before %s\n after  %s", at.name, before, after)
					}
				})
			}
		}
	}
}

func TestWorkflowVersionRepo_WritesOnAMissingOrOtherTenantsVersion_ReturnNotFoundAndTouchNothing(t *testing.T) {
	type write func(ctx context.Context, repo *postgres.WorkflowVersionRepo, tenantID, id uuid.UUID) error
	writes := []struct {
		name string
		run  write
	}{
		{"UpdateDraft", func(ctx context.Context, repo *postgres.WorkflowVersionRepo, tenantID, id uuid.UUID) error {
			v := newDraftVersion(uuid.New(), tenantID)
			v.ID = id
			v.BPMNXML = "<definitions><!-- w4c --></definitions>"
			v.RecordVersion = 1
			return repo.UpdateDraft(ctx, v)
		}},
		{"Publish", func(ctx context.Context, repo *postgres.WorkflowVersionRepo, tenantID, id uuid.UUID) error {
			return repo.Publish(ctx, tenantID, id, 1, 1, `{"steps":[]}`, "w4c-hash", nil)
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
	}
	modes := []struct {
		name string
		call func(t *testing.T, superPool *pgcommon.Pool, dsn string, owner, caller uuid.UUID, ownerVersion uuid.UUID) (pool *pgcommon.Pool, tenantID, id uuid.UUID)
	}{
		{"unknown id", func(_ *testing.T, superPool *pgcommon.Pool, _ string, owner, _ uuid.UUID, _ uuid.UUID) (*pgcommon.Pool, uuid.UUID, uuid.UUID) {
			return superPool, owner, uuid.New()
		}},
		{"other tenant's id under the caller's own tenant id", func(_ *testing.T, superPool *pgcommon.Pool, _ string, _, caller uuid.UUID, ownerVersion uuid.UUID) (*pgcommon.Pool, uuid.UUID, uuid.UUID) {
			return superPool, caller, ownerVersion
		}},
		{"other tenant's id under the owner's tenant id on a pool bound to the caller", func(t *testing.T, _ *pgcommon.Pool, dsn string, owner, caller uuid.UUID, ownerVersion uuid.UUID) (*pgcommon.Pool, uuid.UUID, uuid.UUID) {
			user := uuid.New()
			return fixtures.NewRestrictedRolePool(t, dsn, false, &caller, &user), owner, ownerVersion
		}},
	}
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	for _, w := range writes {
		for _, m := range modes {
			t.Run(w.name+"/"+m.name, func(t *testing.T) {
				owner, caller := uuid.New(), uuid.New()
				wf := w4cWorkflow(t, superPool, owner)
				ownerVersion := w4cVersionInStatus(t, superPool, wf, domain.VersionStatusDraft, 0)
				before := w4cSnapshot(t, ownerVersion)

				pool, tenantID, id := m.call(t, superPool, dsn, owner, caller, ownerVersion.ID)
				err := w.run(context.Background(), postgres.NewWorkflowVersionRepo(pool), tenantID, id)

				if !errors.Is(err, domain.ErrNotFound) {
					t.Fatalf("%s = %v, want ErrNotFound", w.name, err)
				}
				if after := w4cSnapshot(t, w4cStored(t, superPool, owner, ownerVersion.ID)); after != before {
					t.Errorf("the owner's version changed:\n before %s\n after  %s", before, after)
				}
			})
		}
	}
}
