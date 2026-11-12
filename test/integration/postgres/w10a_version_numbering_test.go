//go:build integration

package postgres_test

import (
	"context"
	"net/http"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

func w10aPublishAndArchiveModuleVersion(t *testing.T, repo *pgadapter.ModuleVersionRepo, tenantID uuid.UUID, moduleID uuid.UUID, number int32, archive bool) *domain.ModuleVersion {
	t.Helper()
	ctx := context.Background()
	v := newDraftModuleVersion(moduleID, &tenantID, domain.ScopeTenant)
	require.NoError(t, repo.Create(ctx, v))
	require.NoError(t, repo.Publish(ctx, tenantID, v.ID, number))
	if archive {
		require.NoError(t, repo.Archive(ctx, tenantID, v.ID))
	}
	return v
}

func TestModuleVersionRepo_NextVersionNumber_CountsPublishedAndArchivedVersionsOfThatModuleOnly(t *testing.T) {
	pool := newTestPool(t)
	repo := pgadapter.NewModuleVersionRepo(pool)
	ctx := context.Background()

	t.Run("a module with no version and a module with only a draft start at 1", func(t *testing.T) {
		tenantID := uuid.New()
		empty := newTestModule(t, ctx, pool, tenantID)
		drafted := newTestModule(t, ctx, pool, tenantID)
		require.NoError(t, repo.Create(ctx, newDraftModuleVersion(drafted.ID, &tenantID, domain.ScopeTenant)))

		first, err := repo.NextVersionNumber(ctx, empty.ID)
		require.NoError(t, err)
		second, err := repo.NextVersionNumber(ctx, drafted.ID)
		require.NoError(t, err)

		assert.EqualValues(t, 1, first)
		assert.EqualValues(t, 1, second, "a draft has no number and does not count")
	})

	t.Run("archived versions stay in the maximum", func(t *testing.T) {
		tenantID := uuid.New()
		mod := newTestModule(t, ctx, pool, tenantID)
		other := newTestModule(t, ctx, pool, tenantID)
		w10aPublishAndArchiveModuleVersion(t, repo, tenantID, other.ID, 1, false)
		w10aPublishAndArchiveModuleVersion(t, repo, tenantID, other.ID, 2, false)
		w10aPublishAndArchiveModuleVersion(t, repo, tenantID, other.ID, 3, false)
		w10aPublishAndArchiveModuleVersion(t, repo, tenantID, mod.ID, 1, true)

		afterOne, err := repo.NextVersionNumber(ctx, mod.ID)
		require.NoError(t, err)
		w10aPublishAndArchiveModuleVersion(t, repo, tenantID, mod.ID, 2, true)
		afterTwo, err := repo.NextVersionNumber(ctx, mod.ID)
		require.NoError(t, err)
		require.NoError(t, repo.Create(ctx, newDraftModuleVersion(mod.ID, &tenantID, domain.ScopeTenant)))
		withDraft, err := repo.NextVersionNumber(ctx, mod.ID)
		require.NoError(t, err)

		assert.EqualValues(t, 2, afterOne, "the only version is archived, the next number is still 2")
		assert.EqualValues(t, 3, afterTwo, "versions 1 and 2 archived give 3")
		assert.EqualValues(t, 3, withDraft, "an open draft does not change it, and the other module's versions do not count")
	})

	t.Run("a second publish that computed a number already taken is refused as a draft concurrency conflict", func(t *testing.T) {
		tenantID := uuid.New()
		mod := newTestModule(t, ctx, pool, tenantID)
		w10aPublishAndArchiveModuleVersion(t, repo, tenantID, mod.ID, 1, false)
		loser := newDraftModuleVersion(mod.ID, &tenantID, domain.ScopeTenant)
		require.NoError(t, repo.Create(ctx, loser))

		err := repo.Publish(ctx, tenantID, loser.ID, 1)

		require.ErrorIs(t, err, domain.ErrDraftConcurrency)
		stored, err := repo.GetByID(ctx, tenantID, loser.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.VersionStatusDraft, stored.Status, "the refused publish leaves the draft a draft")
		assert.Nil(t, stored.VersionNumber)

		next, err := repo.NextVersionNumber(ctx, mod.ID)
		require.NoError(t, err)
		require.EqualValues(t, 2, next)
		require.NoError(t, repo.Publish(ctx, tenantID, loser.ID, next), "control: the same draft publishes with the number that is free")
	})
}

type w10aStaleNumberVersions struct {
	port.WorkflowVersionRepository
	number int32
}

func (s w10aStaleNumberVersions) NextVersionNumber(context.Context, uuid.UUID, uuid.UUID) (int32, error) {
	return s.number, nil
}

func TestWorkflowVersionNumbering_ArchivedVersionsCountAndATakenNumberIsADraftConcurrencyConflict(t *testing.T) {
	pool := newTestPool(t)
	vRepo := pgadapter.NewWorkflowVersionRepo(pool)
	ctx := context.Background()
	bpmnXML, err := os.ReadFile("../../unit/service/testdata/single-process.bpmn")
	require.NoError(t, err)

	t.Run("versions 1 and 2 archived give 3 and publishing the next draft answers 200 with number 3 and a new active version", func(t *testing.T) {
		tenantID, userID := uuid.New(), uuid.New()
		wf := seedWorkflow(t, pool, tenantID, "w10a-archived-max")
		for number := int32(1); number <= 2; number++ {
			v := seedDraft(t, pool, wf, "<w10a-old/>")
			publishVersion(t, pool, v, number)
			require.NoError(t, vRepo.Archive(ctx, tenantID, v.ID))
		}
		other := seedWorkflow(t, pool, tenantID, "w10a-archived-max-other")
		otherVersion := seedDraft(t, pool, other, "<w10a-other/>")
		publishVersion(t, pool, otherVersion, 7)
		next, err := vRepo.NextVersionNumber(ctx, tenantID, wf.ID)
		require.NoError(t, err)
		require.EqualValues(t, 3, next, "archived versions stay in the maximum, another workflow's do not")
		draft := seedDraft(t, pool, wf, string(bpmnXML))

		w, resp := w7bPost(w7bRouter(pool, nil), tenantID, userID, wf.ID, draft.ID, "publish", "")

		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.NotNil(t, resp.VersionNumber)
		assert.EqualValues(t, 3, *resp.VersionNumber)
		assert.Equal(t, draft.ID.String(), resp.VersionID)
		stored, err := vRepo.GetByID(ctx, tenantID, draft.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.VersionStatusPublished, stored.Status)
		require.NotNil(t, stored.VersionNumber)
		assert.EqualValues(t, 3, *stored.VersionNumber)
		reloaded, err := pgadapter.NewWorkflowRepo(pool).GetByID(ctx, tenantID, wf.ID)
		require.NoError(t, err)
		require.NotNil(t, reloaded.ActiveVersionID)
		assert.Equal(t, draft.ID, *reloaded.ActiveVersionID, "the new version is the active one")
		require.NotNil(t, reloaded.ActiveVersionNumber)
		assert.EqualValues(t, 3, *reloaded.ActiveVersionNumber)
	})

	t.Run("a publish that computed a number already taken answers 409 DRAFT_CONCURRENCY and changes nothing", func(t *testing.T) {
		tenantID, userID := uuid.New(), uuid.New()
		wf := seedWorkflow(t, pool, tenantID, "w10a-taken-number")
		first := seedDraft(t, pool, wf, "<w10a-first/>")
		publishVersion(t, pool, first, 1)
		draft := seedDraft(t, pool, wf, string(bpmnXML))
		router := w7bRouter(pool, w10aStaleNumberVersions{WorkflowVersionRepository: vRepo, number: 1})

		w, resp := w7bPost(router, tenantID, userID, wf.ID, draft.ID, "publish", "")

		require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
		assert.Equal(t, "DRAFT_CONCURRENCY", resp.Code)
		stored, err := vRepo.GetByID(ctx, tenantID, draft.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.VersionStatusDraft, stored.Status)
		assert.Nil(t, stored.VersionNumber)
		keptFirst, err := vRepo.GetByID(ctx, tenantID, first.ID)
		require.NoError(t, err)
		require.NotNil(t, keptFirst.VersionNumber)
		assert.EqualValues(t, 1, *keptFirst.VersionNumber)
		reloaded, err := pgadapter.NewWorkflowRepo(pool).GetByID(ctx, tenantID, wf.ID)
		require.NoError(t, err)
		assert.Nil(t, reloaded.ActiveVersionID, "the refused publish does not activate anything")

		w, resp = w7bPost(w7bRouter(pool, nil), tenantID, userID, wf.ID, draft.ID, "publish", "")
		require.Equal(t, http.StatusOK, w.Code, "control: with the real number the same draft publishes: %s", w.Body.String())
		require.NotNil(t, resp.VersionNumber)
		assert.EqualValues(t, 2, *resp.VersionNumber)
	})

	t.Run("the repository refuses a second draft published with a number its workflow already holds", func(t *testing.T) {
		tenantID := uuid.New()
		wf := seedWorkflow(t, pool, tenantID, "w10a-taken-number-repo")
		first := seedDraft(t, pool, wf, "<w10a-first/>")
		publishVersion(t, pool, first, 1)
		second := seedDraft(t, pool, wf, "<w10a-second/>")

		err := vRepo.Publish(ctx, tenantID, second.ID, 1, 1, `{}`, "h", nil)

		require.ErrorIs(t, err, domain.ErrDraftConcurrency)
		assert.NotErrorIs(t, err, domain.ErrVersionNotDraft)
	})
}
