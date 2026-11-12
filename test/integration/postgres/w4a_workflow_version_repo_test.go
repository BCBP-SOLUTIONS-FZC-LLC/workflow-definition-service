//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

type w4aNullFlags struct {
	Plan, Hash, Problems bool
}

func w4aReadNullFlags(t *testing.T, pool *pgcommon.Pool, versionID uuid.UUID) w4aNullFlags {
	t.Helper()
	var flags w4aNullFlags
	require.NoError(t, pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		return conn.QueryRow(ctx,
			`SELECT compiled_plan_json IS NULL, artifact_hash IS NULL, validation_errors_json IS NULL
			   FROM workflow_version WHERE id = $1`, versionID).Scan(&flags.Plan, &flags.Hash, &flags.Problems)
	}))
	return flags
}

func w4aSeedDraft(t *testing.T, pool *pgcommon.Pool, tenantID uuid.UUID, key string, mutate func(*domain.WorkflowVersion)) (*domain.Workflow, *domain.WorkflowVersion) {
	t.Helper()
	ctx := context.Background()
	wf := w4aNewWorkflow(tenantID, key)
	require.NoError(t, pgadapter.NewWorkflowRepo(pool).Create(ctx, wf))
	v := newDraftVersion(wf.ID, tenantID)
	if mutate != nil {
		mutate(v)
	}
	require.NoError(t, pgadapter.NewWorkflowVersionRepo(pool).Create(ctx, v))
	return wf, v
}

func TestWorkflowVersionRepo_CreateAndUpdateDraft_EmptyOrNilArtifactsAreStoredAsNullAndReadBackNil(t *testing.T) {
	pool := newTestPool(t)
	repo := pgadapter.NewWorkflowVersionRepo(pool)
	ctx := context.Background()
	empty := ""
	plan, problems := `{"steps":[1]}`, `[{"code":"E1"}]`

	cases := []struct {
		name  string
		plan  *string
		hash  string
		probs *string
	}{
		{"nil pointers and empty hash", nil, "", nil},
		{"empty strings", &empty, "", &empty},
	}
	for _, tc := range cases {
		t.Run("create stores "+tc.name+" as null", func(t *testing.T) {
			tenantID := uuid.New()
			_, v := w4aSeedDraft(t, pool, tenantID, "null-create-"+uuid.NewString(), func(v *domain.WorkflowVersion) {
				v.CompiledPlanJSON, v.ArtifactHash, v.ValidationErrorsJSON = tc.plan, tc.hash, tc.probs
			})

			assert.Equal(t, w4aNullFlags{Plan: true, Hash: true, Problems: true}, w4aReadNullFlags(t, pool, v.ID))
			got, err := repo.GetByID(ctx, tenantID, v.ID)
			require.NoError(t, err)
			assert.Nil(t, got.CompiledPlanJSON)
			assert.Nil(t, got.ValidationErrorsJSON)
			assert.Empty(t, got.ArtifactHash)
		})
	}

	t.Run("create keeps real values and update to empty clears all three", func(t *testing.T) {
		tenantID := uuid.New()
		_, v := w4aSeedDraft(t, pool, tenantID, "null-update-"+uuid.NewString(), func(v *domain.WorkflowVersion) {
			v.CompiledPlanJSON, v.ArtifactHash, v.ValidationErrorsJSON = &plan, "hash-1", &problems
		})
		assert.Equal(t, w4aNullFlags{}, w4aReadNullFlags(t, pool, v.ID), "real values are not nulls")
		held, err := repo.GetByID(ctx, tenantID, v.ID)
		require.NoError(t, err)
		require.NotNil(t, held.CompiledPlanJSON)
		require.NotNil(t, held.ValidationErrorsJSON)
		assert.JSONEq(t, plan, *held.CompiledPlanJSON)
		assert.JSONEq(t, problems, *held.ValidationErrorsJSON)
		assert.Equal(t, "hash-1", held.ArtifactHash)

		held.CompiledPlanJSON, held.ArtifactHash, held.ValidationErrorsJSON = &empty, "", &empty
		require.NoError(t, repo.UpdateDraft(ctx, held))

		assert.Equal(t, w4aNullFlags{Plan: true, Hash: true, Problems: true}, w4aReadNullFlags(t, pool, v.ID))
		got, err := repo.GetByID(ctx, tenantID, v.ID)
		require.NoError(t, err)
		assert.Nil(t, got.CompiledPlanJSON)
		assert.Nil(t, got.ValidationErrorsJSON)
		assert.Empty(t, got.ArtifactHash)
	})
}

func TestWorkflowVersionRepo_Reads_OtherTenantsIdsAreNotFoundOrEmpty(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	ctx := context.Background()
	owner, caller, callerUser := uuid.New(), uuid.New(), uuid.New()
	superRepo := pgadapter.NewWorkflowVersionRepo(superPool)

	module, moduleVersions := libraryModule(t, superPool, nil, "Process_"+uuid.NewString(), 1)
	wf, published := w4aSeedDraft(t, superPool, owner, "other-tenant-reads", nil)
	ref := domain.ModuleRef{ModuleID: module.ID, VersionID: moduleVersions[0].ID, ProcessID: module.ProcessID, VersionNumber: *moduleVersions[0].VersionNumber}
	require.NoError(t, superRepo.Publish(ctx, owner, published.ID, 1, 1, `{}`, "h", []domain.ModuleRef{ref}))
	draft := newDraftVersion(wf.ID, owner)
	require.NoError(t, superRepo.Create(ctx, draft))

	owned, total, err := superRepo.ListByWorkflow(ctx, owner, wf.ID, 1, 10)
	require.NoError(t, err)
	require.EqualValues(t, 2, total, "the owner sees both versions")
	require.Len(t, owned, 2)
	usages, err := superRepo.ListModuleUsages(ctx, owner, module.ID)
	require.NoError(t, err)
	require.Len(t, usages, 1, "the owner sees the module usage")
	got, err := superRepo.GetDraft(ctx, owner, wf.ID)
	require.NoError(t, err)
	require.Equal(t, draft.ID, got.ID)

	callerRepo := pgadapter.NewWorkflowVersionRepo(fixtures.NewRestrictedRolePool(t, dsn, false, &caller, &callerUser))
	for _, asTenant := range []struct {
		name string
		id   uuid.UUID
	}{
		{"as the caller's own tenant id", caller},
		{"under the owner's tenant id through the caller's row-level security", owner},
	} {
		t.Run(asTenant.name, func(t *testing.T) {
			byID, err := callerRepo.GetByID(ctx, asTenant.id, published.ID)
			assert.ErrorIs(t, err, domain.ErrNotFound)
			assert.Nil(t, byID)

			byDraft, err := callerRepo.GetDraft(ctx, asTenant.id, wf.ID)
			assert.ErrorIs(t, err, domain.ErrNotFound)
			assert.Nil(t, byDraft)

			list, listTotal, err := callerRepo.ListByWorkflow(ctx, asTenant.id, wf.ID, 1, 10)
			require.NoError(t, err)
			assert.Empty(t, list)
			assert.Zero(t, listTotal)

			moduleUsages, err := callerRepo.ListModuleUsages(ctx, asTenant.id, module.ID)
			require.NoError(t, err)
			assert.Empty(t, moduleUsages)
		})
	}
}

func TestWorkflowVersionRepo_GetDraft_WorkflowWithOnlyPublishedAndArchivedVersions_ReturnsNotFound(t *testing.T) {
	pool := newTestPool(t)
	repo := pgadapter.NewWorkflowVersionRepo(pool)
	ctx := context.Background()
	tenantID := uuid.New()
	wf, first := w4aSeedDraft(t, pool, tenantID, "only-published-and-archived", nil)
	require.NoError(t, repo.Publish(ctx, tenantID, first.ID, 1, 1, `{}`, "h1", nil))
	second := newDraftVersion(wf.ID, tenantID)
	require.NoError(t, repo.Create(ctx, second))
	require.NoError(t, repo.Publish(ctx, tenantID, second.ID, 1, 2, `{}`, "h2", nil))
	require.NoError(t, repo.Archive(ctx, tenantID, second.ID))
	statuses := map[uuid.UUID]domain.VersionStatus{}
	list, total, err := repo.ListByWorkflow(ctx, tenantID, wf.ID, 1, 10)
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	for _, v := range list {
		statuses[v.ID] = v.Status
	}
	require.Equal(t, map[uuid.UUID]domain.VersionStatus{first.ID: domain.VersionStatusPublished, second.ID: domain.VersionStatusArchived}, statuses)

	got, err := repo.GetDraft(ctx, tenantID, wf.ID)

	assert.ErrorIs(t, err, domain.ErrNotFound)
	assert.Nil(t, got)

	third := newDraftVersion(wf.ID, tenantID)
	require.NoError(t, repo.Create(ctx, third))
	got, err = repo.GetDraft(ctx, tenantID, wf.ID)
	require.NoError(t, err, "a new draft is found once it exists")
	assert.Equal(t, third.ID, got.ID)
}

func TestWorkflowVersionRepo_UpdateDraft_MatchingRecordVersion_StoresEveryColumnBumpsTheVersionAndOverwritesWithNull(t *testing.T) {
	pool := newTestPool(t)
	repo := pgadapter.NewWorkflowVersionRepo(pool)
	ctx := context.Background()
	tenantID := uuid.New()
	oldPlan, oldProblems := `{"steps":["old"]}`, `[{"code":"OLD"}]`
	wf, seeded := w4aSeedDraft(t, pool, tenantID, "update-draft-columns", func(v *domain.WorkflowVersion) {
		v.CompiledPlanJSON, v.ArtifactHash, v.ValidationErrorsJSON = &oldPlan, "old-hash", &oldProblems
	})
	held, err := repo.GetByID(ctx, tenantID, seeded.ID)
	require.NoError(t, err)
	readVersion := held.RecordVersion

	newPlan, newProblems := `{"steps":["new"]}`, `[{"code":"NEW"}]`
	held.BPMNXML = "<definitions><!-- saved --></definitions>"
	held.CompiledPlanJSON, held.ArtifactHash, held.ValidationErrorsJSON = &newPlan, "new-hash", &newProblems
	held.IsValid = false
	require.NoError(t, repo.UpdateDraft(ctx, held))

	assert.Equal(t, readVersion+1, held.RecordVersion, "the caller's struct carries the version the save produced")
	stored, err := repo.GetByID(ctx, tenantID, seeded.ID)
	require.NoError(t, err)
	assert.Equal(t, held.RecordVersion, stored.RecordVersion)
	assert.Equal(t, "<definitions><!-- saved --></definitions>", stored.BPMNXML)
	assert.Equal(t, "new-hash", stored.ArtifactHash)
	assert.False(t, stored.IsValid)
	require.NotNil(t, stored.CompiledPlanJSON)
	require.NotNil(t, stored.ValidationErrorsJSON)
	assert.JSONEq(t, newPlan, *stored.CompiledPlanJSON)
	assert.JSONEq(t, newProblems, *stored.ValidationErrorsJSON)
	assert.True(t, stored.UpdatedAt.After(held.UpdatedAt), "the save moved updated_at past the value read")
	assert.Equal(t, domain.VersionStatusDraft, stored.Status)
	assert.Equal(t, wf.ID, stored.WorkflowID)
	assert.Equal(t, seeded.CreatedByUserID, stored.CreatedByUserID)
	assert.Nil(t, stored.VersionNumber)

	savedAt := stored.UpdatedAt
	stored.CompiledPlanJSON, stored.ArtifactHash, stored.ValidationErrorsJSON = nil, "", nil
	stored.IsValid = true
	require.NoError(t, repo.UpdateDraft(ctx, stored))

	assert.Equal(t, readVersion+2, stored.RecordVersion, "every save counts one")
	assert.Equal(t, w4aNullFlags{Plan: true, Hash: true, Problems: true}, w4aReadNullFlags(t, pool, seeded.ID), "an overwrite with nothing clears the old values")
	after, err := repo.GetByID(ctx, tenantID, seeded.ID)
	require.NoError(t, err)
	assert.Equal(t, readVersion+2, after.RecordVersion)
	assert.True(t, after.IsValid)
	assert.Equal(t, "<definitions><!-- saved --></definitions>", after.BPMNXML, "columns not changed in the second save keep their value")
	assert.True(t, after.UpdatedAt.After(savedAt), "the second save moved updated_at again")
}

func TestWorkflowVersionRepo_UpdateDraft_StaleRecordVersion_ReturnsDraftConcurrencyAndLeavesTheCallersStructAndTheRowAlone(t *testing.T) {
	pool := newTestPool(t)
	repo := pgadapter.NewWorkflowVersionRepo(pool)
	ctx := context.Background()

	cases := []struct {
		name  string
		shift func(heldVersion int64) int64
	}{
		{"version older than the stored one after a competing save", func(held int64) int64 { return held }},
		{"version newer than anything stored", func(held int64) int64 { return held + 5 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenantID := uuid.New()
			_, seeded := w4aSeedDraft(t, pool, tenantID, "update-draft-stale-"+uuid.NewString(), nil)
			held, err := repo.GetByID(ctx, tenantID, seeded.ID)
			require.NoError(t, err)
			rival := *held
			rival.BPMNXML = "<definitions><!-- rival --></definitions>"
			require.NoError(t, repo.UpdateDraft(ctx, &rival))
			stored, err := repo.GetByID(ctx, tenantID, seeded.ID)
			require.NoError(t, err)

			plan := `{"steps":["stale"]}`
			stale := *held
			stale.BPMNXML = "<definitions><!-- stale --></definitions>"
			stale.CompiledPlanJSON, stale.ArtifactHash = &plan, "stale-hash"
			stale.RecordVersion = tc.shift(held.RecordVersion)
			sent := stale

			err = repo.UpdateDraft(ctx, &stale)

			require.ErrorIs(t, err, domain.ErrDraftConcurrency)
			assert.NotErrorIs(t, err, domain.ErrVersionNotDraft)
			assert.NotErrorIs(t, err, domain.ErrNotFound)
			assert.Equal(t, sent, stale, "the caller's struct is not touched by a refused save")
			after, err := repo.GetByID(ctx, tenantID, seeded.ID)
			require.NoError(t, err)
			assert.Equal(t, stored, after, "the stored draft is exactly what the competing save left")
			assert.Equal(t, rival.BPMNXML, after.BPMNXML)
		})
	}
}
