//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	bpmncompiler "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

const w13aSavedXML = `<definitions id="w13a-saved"/>`

func w13aDraftRouter(pool *pgcommon.Pool, versions port.WorkflowVersionRepository) *gin.Engine {
	return w13aDraftRouterWithCache(pool, versions, nil)
}

func w13aDraftRouterWithCache(pool *pgcommon.Pool, versions port.WorkflowVersionRepository, cache port.CacheStore) *gin.Engine {
	if versions == nil {
		versions = postgres.NewWorkflowVersionRepo(pool)
	}
	return w10bEngine(handler.Services{Drafts: service.NewDraftService(service.DraftDeps{
		Cache:     cache,
		Workflows: postgres.NewWorkflowRepo(pool),
		Versions:  versions,
		Compiler:  bpmncompiler.New(),
	})})
}

type w13aLockCache struct {
	port.CacheStore
	mu       sync.Mutex
	held     map[string]string
	acquired int
}

func (c *w13aLockCache) SetNX(_ context.Context, key, value string, _ time.Duration) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, taken := c.held[key]; taken {
		return false, nil
	}
	if c.held == nil {
		c.held = map[string]string{}
	}
	c.held[key] = value
	c.acquired++
	return true, nil
}

func (c *w13aLockCache) DelIfEqual(_ context.Context, key, value string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.held[key] == value {
		delete(c.held, key)
	}
	return nil
}

func (c *w13aLockCache) state() (acquired, stillHeld int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.acquired, len(c.held)
}

func w13aSaveDraft(t *testing.T, router *gin.Engine, tenantID, userID, workflowID uuid.UUID, recordVersion int64) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{"bpmn_xml": w13aSavedXML, "record_version": recordVersion})
	require.NoError(t, err)
	return w10bCall(router, http.MethodPut, "/api/v1/workflows/"+workflowID.String()+"/draft", tenantID, userID, string(body))
}

func w13aDiscardDraft(router *gin.Engine, tenantID, userID, workflowID uuid.UUID) *httptest.ResponseRecorder {
	return w10bCall(router, http.MethodDelete, "/api/v1/workflows/"+workflowID.String()+"/draft", tenantID, userID, "")
}

func w13aRequireProblem(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantCode, wantPath string) w10bProblem {
	t.Helper()
	require.Equal(t, wantStatus, rec.Code, rec.Body.String())
	p := w10bDecodeProblem(t, rec)
	assert.Equal(t, wantStatus, p.Status)
	assert.Equal(t, wantCode, p.Code)
	assert.Equal(t, wantPath, p.Instance)
	return p
}

func w13aAssertDraftUnchanged(t *testing.T, pool *pgcommon.Pool, held *domain.WorkflowVersion) {
	t.Helper()
	after := w4cStored(t, pool, held.TenantID, held.ID)
	assert.Equal(t, domain.VersionStatusDraft, after.Status)
	assert.Equal(t, held.BPMNXML, after.BPMNXML, "the refused save left the diagram alone")
	assert.Equal(t, held.RecordVersion, after.RecordVersion, "the refused save did not bump the record version")
}

func TestUpdateDraft_HTTP_ADatabaseFailureOnTheDraftReadOrWrite_IsMappedAndTheDraftIsUnchanged(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)

	cases := []struct {
		name       string
		pool       func(t *testing.T, tenantID, userID uuid.UUID) (*pgcommon.Pool, *sgbCuttingProxy)
		wantStatus int
		wantType   string
		wantCode   string
		wantDetail string
		wantCut    bool
	}{
		{
			name: "the connection is lost during the draft read",
			pool: func(t *testing.T, _, _ uuid.UUID) (*pgcommon.Pool, *sgbCuttingProxy) {
				return w10bCutPool(t, dsn, "name: GetDraftVersion")
			},
			wantStatus: http.StatusServiceUnavailable,
			wantType:   w10bProblemUnavailable,
			wantCode:   "UPSTREAM_UNAVAILABLE",
			wantDetail: "an upstream dependency is temporarily unavailable",
			wantCut:    true,
		},
		{
			name: "the pool is closed when the draft is read",
			pool: func(t *testing.T, _, _ uuid.UUID) (*pgcommon.Pool, *sgbCuttingProxy) {
				closed := newTestPool(t)
				closed.Close()
				return closed, nil
			},
			wantStatus: http.StatusServiceUnavailable,
			wantType:   w10bProblemUnavailable,
			wantCode:   "UPSTREAM_UNAVAILABLE",
			wantDetail: "an upstream dependency is temporarily unavailable",
		},
		{
			name: "the draft update is refused by the database",
			pool: func(t *testing.T, tenantID, userID uuid.UUID) (*pgcommon.Pool, *sgbCuttingProxy) {
				return w10bRestrictedPool(t, superPool, dsn, tenantID, userID, "REVOKE UPDATE ON workflow_version FROM {role}"), nil
			},
			wantStatus: http.StatusInternalServerError,
			wantType:   w10bProblemInternal,
			wantCode:   "INTERNAL_ERROR",
			wantDetail: "an unexpected error occurred",
		},
		{
			name: "the connection is lost during the draft update",
			pool: func(t *testing.T, _, _ uuid.UUID) (*pgcommon.Pool, *sgbCuttingProxy) {
				return w10bCutPool(t, dsn, "name: UpdateDraftVersion")
			},
			wantStatus: http.StatusServiceUnavailable,
			wantType:   w10bProblemUnavailable,
			wantCode:   "UPSTREAM_UNAVAILABLE",
			wantDetail: "an upstream dependency is temporarily unavailable",
			wantCut:    true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenantID, userID := uuid.New(), uuid.New()
			wf, held := seedDraftUpdateWorkflow(t, superPool, tenantID, "w13a")
			pool, proxy := tc.pool(t, tenantID, userID)
			locks := &w13aLockCache{}
			router := w13aDraftRouterWithCache(pool, nil, locks)

			rec := w13aSaveDraft(t, router, tenantID, userID, wf.ID, held.RecordVersion)

			path := "/api/v1/workflows/" + wf.ID.String() + "/draft"
			p := w13aRequireProblem(t, rec, tc.wantStatus, tc.wantCode, path)
			assert.Equal(t, tc.wantType, p.Type)
			assert.Equal(t, tc.wantDetail, p.Detail, "the database's own message stays out of the response")
			assert.NotContains(t, rec.Body.String(), "permission denied")
			if tc.wantCut {
				require.NotNil(t, proxy)
				assert.Equal(t, 1, proxy.cutCount(), "the failing statement is the one that lost its connection, and it is not retried")
			}
			acquired, stillHeld := locks.state()
			assert.Equal(t, 1, acquired, "the save took the draft lock")
			assert.Zero(t, stillHeld, "the draft lock is released after the failure")
			w13aAssertDraftUnchanged(t, superPool, held)
		})
	}
}

func TestUpdateDraft_HTTP_DraftOfAnotherTenant_IsNotFoundUnderRowLevelSecurityAndStaysUntouched(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	owner, ownerUser := uuid.New(), uuid.New()
	caller, callerUser := uuid.New(), uuid.New()
	callerPool := fixtures.NewRestrictedRolePool(t, dsn, false, &caller, &callerUser)
	ownerPool := fixtures.NewRestrictedRolePool(t, dsn, false, &owner, &ownerUser)
	wf, held := seedDraftUpdateWorkflow(t, superPool, owner, "w13a")
	path := "/api/v1/workflows/" + wf.ID.String() + "/draft"
	callerRouter := w13aDraftRouter(callerPool, nil)

	for _, asTenant := range []struct {
		name string
		id   uuid.UUID
	}{
		{"the caller names its own tenant", caller},
		{"the caller names the owner's tenant on a pool bound to the caller", owner},
	} {
		t.Run(asTenant.name, func(t *testing.T) {
			rec := w13aSaveDraft(t, callerRouter, asTenant.id, callerUser, wf.ID, held.RecordVersion)

			p := w13aRequireProblem(t, rec, http.StatusNotFound, "NOT_FOUND", path)
			assert.Equal(t, w10bProblemNotFound, p.Type)
			assert.Equal(t, "resource not found", p.Detail)
			w13aAssertDraftUnchanged(t, superPool, held)
		})
	}

	t.Run("the draft update statement run on a pool bound to the caller with the owner's tenant changes nothing", func(t *testing.T) {
		v := *held
		v.TenantID = owner
		v.BPMNXML = w13aSavedXML

		err := postgres.NewWorkflowVersionRepo(callerPool).UpdateDraft(context.Background(), &v)

		require.ErrorIs(t, err, domain.ErrNotFound)
		w13aAssertDraftUnchanged(t, superPool, held)
	})

	t.Run("the owner saves the same draft on its own pool", func(t *testing.T) {
		rec := w13aSaveDraft(t, w13aDraftRouter(ownerPool, nil), owner, ownerUser, wf.ID, held.RecordVersion)

		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, w13aSavedXML, w10bReadVersion(t, superPool, held.ID).BPMN)
	})
}

type w13aRacingDraftVersions struct {
	port.WorkflowVersionRepository
	beforeSave func()
}

func (r *w13aRacingDraftVersions) UpdateDraft(ctx context.Context, v *domain.WorkflowVersion) error {
	r.beforeSave()
	return r.WorkflowVersionRepository.UpdateDraft(ctx, v)
}

func TestUpdateDraft_HTTP_TheDraftChangesBetweenTheReadAndTheSave_AnswersWhyTheSaveWasRefused(t *testing.T) {
	cases := []struct {
		name        string
		change      func(t *testing.T, repo *postgres.WorkflowVersionRepo, held *domain.WorkflowVersion)
		wantStatus  int
		wantCode    string
		wantStatus2 string
		wantGone    bool
	}{
		{
			name: "it was published",
			change: func(t *testing.T, repo *postgres.WorkflowVersionRepo, held *domain.WorkflowVersion) {
				require.NoError(t, repo.Publish(context.Background(), held.TenantID, held.ID, held.RecordVersion, 1, `{"steps":[]}`, "w13a-hash", nil))
			},
			wantStatus:  http.StatusConflict,
			wantCode:    "INVALID_VERSION_STATUS",
			wantStatus2: "PUBLISHED",
		},
		{
			name: "it was published and archived",
			change: func(t *testing.T, repo *postgres.WorkflowVersionRepo, held *domain.WorkflowVersion) {
				ctx := context.Background()
				require.NoError(t, repo.Publish(ctx, held.TenantID, held.ID, held.RecordVersion, 1, `{"steps":[]}`, "w13a-hash", nil))
				require.NoError(t, repo.Archive(ctx, held.TenantID, held.ID))
			},
			wantStatus:  http.StatusConflict,
			wantCode:    "INVALID_VERSION_STATUS",
			wantStatus2: "ARCHIVED",
		},
		{
			name: "it was deleted",
			change: func(t *testing.T, repo *postgres.WorkflowVersionRepo, held *domain.WorkflowVersion) {
				require.NoError(t, repo.DeleteDraft(context.Background(), held.TenantID, held.ID))
			},
			wantStatus: http.StatusNotFound,
			wantCode:   "NOT_FOUND",
			wantGone:   true,
		},
		{
			name: "another editor saved it",
			change: func(t *testing.T, repo *postgres.WorkflowVersionRepo, held *domain.WorkflowVersion) {
				other := *held
				other.BPMNXML = `<definitions id="w13a-rival"/>`
				require.NoError(t, repo.UpdateDraft(context.Background(), &other))
			},
			wantStatus:  http.StatusConflict,
			wantCode:    "DRAFT_CONCURRENCY",
			wantStatus2: "DRAFT",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := newTestPool(t)
			ctx := context.Background()
			tenantID, userID := uuid.New(), uuid.New()
			wf, held := seedDraftUpdateWorkflow(t, pool, tenantID, "w13a")
			repo := postgres.NewWorkflowVersionRepo(pool)
			router := w13aDraftRouter(pool, &w13aRacingDraftVersions{
				WorkflowVersionRepository: repo,
				beforeSave:                func() { tc.change(t, repo, held) },
			})

			rec := w13aSaveDraft(t, router, tenantID, userID, wf.ID, held.RecordVersion)

			w13aRequireProblem(t, rec, tc.wantStatus, tc.wantCode, "/api/v1/workflows/"+wf.ID.String()+"/draft")
			if tc.wantGone {
				_, err := repo.GetByID(ctx, tenantID, held.ID)
				assert.ErrorIs(t, err, domain.ErrNotFound)
				return
			}
			stored := w4cStored(t, pool, tenantID, held.ID)
			assert.Equal(t, domain.VersionStatus(tc.wantStatus2), stored.Status)
			assert.NotEqual(t, w13aSavedXML, stored.BPMNXML, "the refused save did not overwrite the diagram")
			if tc.wantCode == "DRAFT_CONCURRENCY" {
				var withVersion struct {
					Current *int64 `json:"current_record_version"`
				}
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &withVersion))
				require.NotNil(t, withVersion.Current)
				assert.Equal(t, stored.RecordVersion, *withVersion.Current, "the answer carries the record version to retry with")
				assert.Greater(t, stored.RecordVersion, held.RecordVersion)
			}
		})
	}
}

func TestDiscardDraft_HTTP_WorkflowWithADraftAndPublishedAndArchivedVersions_DeletesOnlyTheDraft(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	repo := postgres.NewWorkflowVersionRepo(pool)
	tenantID, userID := uuid.New(), uuid.New()
	wf := w4cWorkflow(t, pool, tenantID)
	archived := w4cVersionInStatus(t, pool, wf, domain.VersionStatusArchived, 1)
	published := w4cVersionInStatus(t, pool, wf, domain.VersionStatusPublished, 2)
	draft := w4cVersionInStatus(t, pool, wf, domain.VersionStatusDraft, 0)
	sameTenantWf := w4cWorkflow(t, pool, tenantID)
	sameTenantDraft := w4cVersionInStatus(t, pool, sameTenantWf, domain.VersionStatusDraft, 0)
	before := map[uuid.UUID]string{}
	for _, v := range []*domain.WorkflowVersion{archived, published, sameTenantDraft} {
		before[v.ID] = w4cSnapshot(t, v)
	}
	router := w13aDraftRouter(pool, nil)

	rec := w13aDiscardDraft(router, tenantID, userID, wf.ID)

	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	assert.Empty(t, rec.Body.String())
	_, err := repo.GetByID(ctx, tenantID, draft.ID)
	assert.ErrorIs(t, err, domain.ErrNotFound, "the draft is gone")
	_, err = repo.GetDraft(ctx, tenantID, wf.ID)
	assert.ErrorIs(t, err, domain.ErrNotFound)
	for name, v := range map[string]*domain.WorkflowVersion{
		"the archived version":                        archived,
		"the published version":                       published,
		"another workflow's draft in the same tenant": sameTenantDraft,
	} {
		assert.Equal(t, before[v.ID], w4cSnapshot(t, w4cStored(t, pool, v.TenantID, v.ID)), "%s is untouched", name)
	}
	_, total, err := repo.ListByWorkflow(ctx, tenantID, wf.ID, 1, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total, "the published and the archived version remain")
}

func TestDiscardDraft_HTTP_DraftOfAnotherTenant_IsNotFoundUnderRowLevelSecurityAndStaysUntouched(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	owner, ownerUser := uuid.New(), uuid.New()
	caller, callerUser := uuid.New(), uuid.New()
	callerPool := fixtures.NewRestrictedRolePool(t, dsn, false, &caller, &callerUser)
	ownerPool := fixtures.NewRestrictedRolePool(t, dsn, false, &owner, &ownerUser)
	wf, held := seedDraftUpdateWorkflow(t, superPool, owner, "w13a")
	path := "/api/v1/workflows/" + wf.ID.String() + "/draft"
	callerRouter := w13aDraftRouter(callerPool, nil)

	for _, asTenant := range []struct {
		name string
		id   uuid.UUID
	}{
		{"the caller names its own tenant", caller},
		{"the caller names the owner's tenant on a pool bound to the caller", owner},
	} {
		t.Run(asTenant.name, func(t *testing.T) {
			rec := w13aDiscardDraft(callerRouter, asTenant.id, callerUser, wf.ID)

			p := w13aRequireProblem(t, rec, http.StatusNotFound, "NOT_FOUND", path)
			assert.Equal(t, w10bProblemNotFound, p.Type)
			assert.Equal(t, "resource not found", p.Detail)
			w13aAssertDraftUnchanged(t, superPool, held)
		})
	}

	t.Run("the draft delete statement run on a pool bound to the caller with the owner's tenant deletes nothing", func(t *testing.T) {
		err := postgres.NewWorkflowVersionRepo(callerPool).DeleteDraft(context.Background(), owner, held.ID)

		require.ErrorIs(t, err, domain.ErrNotFound)
		w13aAssertDraftUnchanged(t, superPool, held)
	})

	t.Run("the owner discards the same draft on its own pool", func(t *testing.T) {
		rec := w13aDiscardDraft(w13aDraftRouter(ownerPool, nil), owner, ownerUser, wf.ID)

		require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
		_, err := postgres.NewWorkflowVersionRepo(superPool).GetByID(context.Background(), owner, held.ID)
		assert.ErrorIs(t, err, domain.ErrNotFound)
	})
}

func TestInitDraft_HTTP_TheDraftIsStoredWithSQLNullForEveryOptionalColumnEvenWhenTheActiveVersionHasThem(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	tenantID, userID := uuid.New(), uuid.New()
	workflows := postgres.NewWorkflowRepo(pool)
	versions := postgres.NewWorkflowVersionRepo(pool)
	wf := w4cWorkflow(t, pool, tenantID)
	plan, problems := `{"steps":["w13a"]}`, `[{"code":"E1"}]`
	number, publishedAt := int32(3), time.Now().UTC().Truncate(time.Microsecond)
	active := newDraftVersion(wf.ID, tenantID)
	active.Status = domain.VersionStatusPublished
	active.CompiledPlanJSON = &plan
	active.ArtifactHash = "w13a-hash"
	active.VersionNumber = &number
	active.PublishedAt = &publishedAt
	active.ValidationErrorsJSON = &problems
	require.NoError(t, versions.Create(ctx, active))
	require.NoError(t, workflows.UpdateActiveVersion(ctx, tenantID, wf.ID, &active.ID))
	seeded := w4cRaw(t, pool, active.ID)
	require.False(t, seeded.PlanNull || seeded.HashNull || seeded.NumberNull || seeded.ErrorsNull || seeded.PublishedNull,
		"the active version really has every optional column set")
	router := w13aDraftRouter(pool, nil)

	rec := w10bCall(router, http.MethodPost, "/api/v1/workflows/"+wf.ID.String()+"/draft", tenantID, userID, "")

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var body struct {
		VersionID uuid.UUID `json:"version_id"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.NotEqual(t, active.ID, body.VersionID)
	raw := w4cRaw(t, pool, body.VersionID)
	assert.True(t, raw.PlanNull, "no compiled plan is stored")
	assert.True(t, raw.HashNull, "no artifact hash is stored")
	assert.True(t, raw.NumberNull, "no version number is stored")
	assert.True(t, raw.PublishedNull, "no publication time is stored")
	assert.True(t, raw.ErrorsNull, "no validation errors are stored")
	read, err := versions.GetDraft(ctx, tenantID, wf.ID)
	require.NoError(t, err)
	assert.Equal(t, body.VersionID, read.ID)
	assert.Nil(t, read.CompiledPlanJSON)
	assert.Empty(t, read.ArtifactHash)
	assert.Nil(t, read.VersionNumber)
	assert.Nil(t, read.PublishedAt)
	assert.Nil(t, read.ValidationErrorsJSON)
}
