//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

func w10bModuleRouter(
	tenantPool, systemPool *pgcommon.Pool,
	wrapModules func(port.ModuleRepository) port.ModuleRepository,
	wrapVersions func(port.ModuleVersionRepository) port.ModuleVersionRepository,
) *gin.Engine {
	var modules port.ModuleRepository = postgres.NewModuleRepo(tenantPool)
	if wrapModules != nil {
		modules = wrapModules(modules)
	}
	var versions port.ModuleVersionRepository = postgres.NewModuleVersionRepo(tenantPool)
	if wrapVersions != nil {
		versions = wrapVersions(versions)
	}
	return w10bEngine(handler.Services{Modules: service.NewModuleService(service.ModuleDeps{
		Transactor:       postgres.NewTransactor(tenantPool),
		GlobalTransactor: postgres.NewTransactor(systemPool),
		Modules:          modules,
		GlobalModules:    postgres.NewModuleRepo(systemPool),
		Versions:         versions,
		GlobalVersions:   postgres.NewModuleVersionRepo(systemPool),
		WorkflowVersions: postgres.NewWorkflowVersionRepo(tenantPool),
		Compiler:         bpmn_compiler.New(),
	})})
}

type w10bModulePage struct {
	Modules []struct {
		Name string `json:"name"`
	} `json:"modules"`
	Pagination struct {
		TotalCount int64 `json:"total_count"`
		Page       int   `json:"page"`
		Limit      int   `json:"limit"`
	} `json:"pagination"`
}

func (p w10bModulePage) names() []string {
	out := make([]string, len(p.Modules))
	for i, m := range p.Modules {
		out[i] = m.Name
	}
	return out
}

func w10bListModules(t *testing.T, router *gin.Engine, tenantID, userID uuid.UUID, query url.Values) (*httptest.ResponseRecorder, w10bModulePage) {
	t.Helper()
	rec := w10bCall(router, http.MethodGet, "/api/v1/modules?"+query.Encode(), tenantID, userID, "")
	var page w10bModulePage
	if rec.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page), rec.Body.String())
	}
	return rec, page
}

func TestListModules_HTTP_ClosedPool_Is503NotAnEmptyList(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	systemPool := fixtures.NewRestrictedRolePool(t, dsn, true, nil, nil)
	tenantID, userID := uuid.New(), uuid.New()
	w4aCreateModule(t, superPool, nil, "w10b-global", w4aModuleEpoch)
	tenantPool := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantID, &userID)
	router := w10bModuleRouter(tenantPool, systemPool, nil, nil)
	_, before := w10bListModules(t, router, tenantID, userID, url.Values{})
	require.Equal(t, []string{"w10b-global"}, before.names(), "the list works before the pool is closed")
	tenantPool.Close()

	t.Run("the repository reports the outage and returns nothing", func(t *testing.T) {
		got, total, err := postgres.NewModuleRepo(tenantPool).List(context.Background(), tenantID, port.ModuleFilter{})

		require.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
		assert.NotErrorIs(t, err, domain.ErrNotFound)
		assert.Nil(t, got)
		assert.Zero(t, total)
	})

	t.Run("the route answers 503", func(t *testing.T) {
		rec, _ := w10bListModules(t, router, tenantID, userID, url.Values{})

		require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
		p := w10bDecodeProblem(t, rec)
		assert.Equal(t, w10bProblemUnavailable, p.Type)
		assert.Equal(t, http.StatusServiceUnavailable, p.Status)
		assert.Equal(t, "UPSTREAM_UNAVAILABLE", p.Code)
		assert.Equal(t, "an upstream dependency is temporarily unavailable", p.Detail)
		assert.Equal(t, "/api/v1/modules", p.Instance)
	})
}

func TestListModules_HTTP_TheCountQueryIsRefusedByTheDatabase_Is500WithoutTheCause(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	systemPool := fixtures.NewRestrictedRolePool(t, dsn, true, nil, nil)
	tenantID, userID := uuid.New(), uuid.New()
	w4aCreateModule(t, superPool, &tenantID, "w10b-own", w4aModuleEpoch)
	tenantPool := w10bRestrictedPool(t, superPool, dsn, tenantID, userID, "REVOKE SELECT ON workflow_module FROM {role}")

	t.Run("the repository stops at the count and classifies nothing as an outage", func(t *testing.T) {
		got, total, err := postgres.NewModuleRepo(tenantPool).List(context.Background(), tenantID, port.ModuleFilter{})

		require.Error(t, err)
		assert.NotErrorIs(t, err, domain.ErrUpstreamUnavailable)
		assert.NotErrorIs(t, err, domain.ErrNotFound)
		assert.Nil(t, got)
		assert.Zero(t, total)
	})

	t.Run("the route answers 500 and hides the cause", func(t *testing.T) {
		router := w10bModuleRouter(tenantPool, systemPool, nil, nil)

		rec, _ := w10bListModules(t, router, tenantID, userID, url.Values{})

		require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
		p := w10bDecodeProblem(t, rec)
		assert.Equal(t, w10bProblemInternal, p.Type)
		assert.Equal(t, "INTERNAL_ERROR", p.Code)
		assert.Equal(t, "an unexpected error occurred", p.Detail)
		assert.NotContains(t, rec.Body.String(), "permission denied")
		assert.NotContains(t, rec.Body.String(), "workflow_module")
		assert.NotContains(t, rec.Body.String(), "list modules")
	})

	t.Run("a refused data query is reported from the second query, not the count", func(t *testing.T) {
		dataOnly := w10bRestrictedPool(t, superPool, dsn, tenantID, userID,
			"REVOKE SELECT ON workflow_module FROM {role}",
			"GRANT SELECT (scope, tenant_id) ON workflow_module TO {role}")

		_, _, err := postgres.NewModuleRepo(dataOnly).List(context.Background(), tenantID, port.ModuleFilter{})

		require.Error(t, err)
	})
}

func TestListModules_HTTP_AsTenantAWithTenantBModulesPresent_ListsOnlyGlobalAndOwnModulesAndCountsThem(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	systemPool := fixtures.NewRestrictedRolePool(t, dsn, true, nil, nil)
	tenantA, tenantB, userA := uuid.New(), uuid.New(), uuid.New()
	w4aCreateModule(t, superPool, nil, "w10b-shared-global", w4aModuleEpoch.Add(1*time.Hour))
	w4aCreateModule(t, superPool, &tenantA, "w10b-shared-a", w4aModuleEpoch.Add(2*time.Hour))
	w4aCreateModule(t, superPool, &tenantA, "w10b-only-a", w4aModuleEpoch.Add(3*time.Hour))
	w4aCreateModule(t, superPool, &tenantB, "w10b-shared-b", w4aModuleEpoch.Add(4*time.Hour))
	w4aCreateModule(t, superPool, &tenantB, "w10b-only-b", w4aModuleEpoch.Add(5*time.Hour))
	poolA := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantA, &userA)
	router := w10bModuleRouter(poolA, systemPool, nil, nil)

	t.Run("the repository bound to tenant A, asked about tenant B, sees only the global module", func(t *testing.T) {
		repo := postgres.NewModuleRepo(poolA)

		gotB, totalB, err := repo.List(context.Background(), tenantB, port.ModuleFilter{})
		require.NoError(t, err)
		assert.Equal(t, []string{"w10b-shared-global"}, w4aModuleNames(gotB), "tenant B's modules stay hidden from a pool bound to tenant A")
		assert.EqualValues(t, 1, totalB)

		gotA, totalA, err := repo.List(context.Background(), tenantA, port.ModuleFilter{})
		require.NoError(t, err)
		assert.Equal(t, []string{"w10b-only-a", "w10b-shared-a", "w10b-shared-global"}, w4aModuleNames(gotA), "control: tenant A's own id lists its modules")
		assert.EqualValues(t, 3, totalA)
	})

	cases := []struct {
		name      string
		query     url.Values
		wantNames []string
	}{
		{"no filter", url.Values{}, []string{"w10b-only-a", "w10b-shared-a", "w10b-shared-global"}},
		{"search text that tenant B's modules also match", url.Values{"q": {"shared"}}, []string{"w10b-shared-a", "w10b-shared-global"}},
		{"scope tenant", url.Values{"scope": {"tenant"}}, []string{"w10b-only-a", "w10b-shared-a"}},
		{"scope global", url.Values{"scope": {"global"}}, []string{"w10b-shared-global"}},
		{"search text only tenant B's modules match", url.Values{"q": {"only-b"}}, []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, page := w10bListModules(t, router, tenantA, userA, tc.query)

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.Equal(t, tc.wantNames, page.names())
			assert.EqualValues(t, len(tc.wantNames), page.Pagination.TotalCount, "tenant B's modules are not counted either")
		})
	}
}

func TestModuleRepo_List_SecondPage_IsNewestFirstWithTheIDAsTieBreakAndTheTotalIsTheWholeFilteredCount(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewModuleRepo(pool)
	tenantID, other := uuid.New(), uuid.New()
	moments := []time.Time{
		w4aModuleEpoch, w4aModuleEpoch, w4aModuleEpoch.Add(time.Hour), w4aModuleEpoch.Add(time.Hour),
		w4aModuleEpoch.Add(time.Hour), w4aModuleEpoch.Add(2 * time.Hour), w4aModuleEpoch.Add(2 * time.Hour),
	}
	var matching []*domain.Module
	for i, at := range moments {
		owner := &tenantID
		if i == 0 {
			owner = nil
		}
		matching = append(matching, w4aCreateModule(t, pool, owner, "w10b-page-a-"+string(rune('a'+i)), at))
	}
	w4aCreateModule(t, pool, &tenantID, "w10b-page-b-one", w4aModuleEpoch.Add(3*time.Hour))
	w4aCreateModule(t, pool, &other, "w10b-page-a-foreign", w4aModuleEpoch.Add(4*time.Hour))
	sorted := func(modules []*domain.Module, createdAt map[uuid.UUID]time.Time) []string {
		ordered := append([]*domain.Module(nil), modules...)
		sort.SliceStable(ordered, func(i, j int) bool {
			ti, tj := createdAt[ordered[i].ID], createdAt[ordered[j].ID]
			if !ti.Equal(tj) {
				return ti.After(tj)
			}
			return strings.Compare(ordered[i].ID.String(), ordered[j].ID.String()) > 0
		})
		names := make([]string, len(ordered))
		for i, m := range ordered {
			names[i] = m.Name
		}
		return names
	}
	createdAt := make(map[uuid.UUID]time.Time)
	for i, m := range matching {
		createdAt[m.ID] = moments[i]
	}
	allSorted := append([]string{"w10b-page-b-one"}, sorted(matching, createdAt)...)
	search := "page-a"

	cases := []struct {
		name      string
		filter    port.ModuleFilter
		wantNames []string
		wantTotal int64
	}{
		{"second page of three with a search", port.ModuleFilter{Page: 2, Limit: 3, Search: &search}, sorted(matching, createdAt)[3:6], int64(len(matching))},
		{"second page of two with a search", port.ModuleFilter{Page: 2, Limit: 2, Search: &search}, sorted(matching, createdAt)[2:4], int64(len(matching))},
		{"last, partly filled page with a search", port.ModuleFilter{Page: 3, Limit: 3, Search: &search}, sorted(matching, createdAt)[6:], int64(len(matching))},
		{"second page of four without a search", port.ModuleFilter{Page: 2, Limit: 4}, allSorted[4:8], int64(len(matching) + 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, total, err := repo.List(context.Background(), tenantID, tc.filter)

			require.NoError(t, err)
			assert.Equal(t, tc.wantNames, w4aModuleNames(got))
			assert.Equal(t, tc.wantTotal, total, "the total is the whole filtered count, not the page size")
		})
	}
}

func w10bModuleRows(t *testing.T, pool *pgcommon.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		return conn.QueryRow(ctx, query, args...).Scan(&n)
	}))
	return n
}

type w10bBlindProcessIDCheck struct {
	port.ModuleRepository
}

func (w10bBlindProcessIDCheck) ProcessIDInUse(context.Context, *uuid.UUID, string) (bool, error) {
	return false, nil
}

func TestCreateModule_HTTP_TwoCreatesOfOneProcessID_OneIsCreatedAndTheOtherIsAConflictWithNoRowsLeftBehind(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	systemPool := fixtures.NewRestrictedRolePool(t, dsn, true, nil, nil)

	t.Run("the process id is taken between the check and the insert", func(t *testing.T) {
		tenantID, userID := uuid.New(), uuid.New()
		tenantPool := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantID, &userID)
		const processID = "Process_W10bCheckThenInsert"
		plain := w10bModuleRouter(tenantPool, systemPool, nil, nil)
		firstModule, _ := w9cCreateModule(t, plain, tenantID, userID, processID)
		blind := w10bModuleRouter(tenantPool, systemPool, func(real port.ModuleRepository) port.ModuleRepository {
			return w10bBlindProcessIDCheck{ModuleRepository: real}
		}, nil)
		body, _ := json.Marshal(map[string]any{"name": "w10b late", "bpmn_xml": w9cModuleXML(processID)})

		rec := w10bCall(blind, http.MethodPost, "/api/v1/modules", tenantID, userID, string(body))

		require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
		p := w10bDecodeProblem(t, rec)
		assert.Equal(t, w10bProblemConflict, p.Type)
		assert.Equal(t, "MODULE_PROCESS_ID_CONFLICT", p.Code)
		assert.Equal(t, domain.ErrModuleProcessIDTaken.Error(), p.Detail)
		assert.Equal(t, "/api/v1/modules", p.Instance)
		assert.Equal(t, 1, w10bModuleRows(t, superPool, `SELECT count(*) FROM workflow_module WHERE tenant_id = $1 AND process_id = $2`, tenantID, processID))
		assert.Equal(t, 1, w9cCountVersions(t, superPool, firstModule), "the first module keeps its single version")
		assert.Equal(t, 1, w10bModuleRows(t, superPool, `SELECT count(*) FROM workflow_module_version WHERE tenant_id = $1 AND process_id = $2`, tenantID, processID))
	})
}

func TestListModuleUsages_HTTP_DraftPublishedAndArchivedWorkflowVersions_EachReportsItsStoredStatus(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	systemPool := fixtures.NewRestrictedRolePool(t, dsn, true, nil, nil)
	ctx := context.Background()
	tenantID, userID := uuid.New(), uuid.New()
	module, moduleVersions := libraryModule(t, superPool, nil, "Process_W10bUsage", 1)
	refs := []domain.ModuleRef{{
		ModuleID: module.ID, VersionID: moduleVersions[0].ID, ProcessID: module.ProcessID, VersionNumber: *moduleVersions[0].VersionNumber,
	}}
	versionsRepo := postgres.NewWorkflowVersionRepo(superPool)
	newWorkflow := func(name string) (*domain.Workflow, *domain.WorkflowVersion) {
		wf := &domain.Workflow{ID: uuid.New(), TenantID: tenantID, CreatedByUserID: userID, TemplateKey: name, Name: name}
		require.NoError(t, postgres.NewWorkflowRepo(superPool).Create(ctx, wf))
		v := newDraftVersion(wf.ID, tenantID)
		require.NoError(t, versionsRepo.Create(ctx, v))
		return wf, v
	}
	_, published := newWorkflow("a-published")
	require.NoError(t, versionsRepo.Publish(ctx, tenantID, published.ID, 1, 1, `{}`, "h", refs))
	_, draft := newWorkflow("b-draft")
	rawRefs, err := json.Marshal(refs)
	require.NoError(t, err)
	require.NoError(t, superPool.WithConn(ctx, func(ctx context.Context, conn *pgxpool.Conn) error {
		_, err := conn.Exec(ctx, `UPDATE workflow_version SET module_refs = $2::jsonb WHERE id = $1`, draft.ID, string(rawRefs))
		return err
	}))
	_, archived := newWorkflow("c-archived")
	require.NoError(t, versionsRepo.Publish(ctx, tenantID, archived.ID, 1, 1, `{}`, "h", refs))
	require.NoError(t, versionsRepo.Archive(ctx, tenantID, archived.ID))
	tenantPool := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantID, &userID)
	router := w10bModuleRouter(tenantPool, systemPool, nil, nil)

	rec := w10bCall(router, http.MethodGet, "/api/v1/modules/"+module.ID.String()+"/usages", tenantID, userID, "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body struct {
		Usages []struct {
			WorkflowKey         string  `json:"workflow_key"`
			VersionID           string  `json:"version_id"`
			VersionNumber       *int32  `json:"version_number"`
			Status              string  `json:"status"`
			ModuleVersionNumber int32   `json:"module_version_number"`
			Behind              bool    `json:"behind"`
			Unused              *string `json:"unused"`
		} `json:"usages"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())
	require.Len(t, body.Usages, 3, "every workflow version that holds the module is listed, whatever its status")
	wantKeys := []string{"a-published", "b-draft", "c-archived"}
	wantStatuses := []string{"PUBLISHED", "DRAFT", "ARCHIVED"}
	wantIDs := []uuid.UUID{published.ID, draft.ID, archived.ID}
	for i, u := range body.Usages {
		assert.Equal(t, wantKeys[i], u.WorkflowKey)
		assert.Equal(t, wantIDs[i].String(), u.VersionID)
		assert.Equal(t, wantStatuses[i], u.Status)
		assert.Equal(t, w10bReadVersion(t, superPool, wantIDs[i]).Status, u.Status, "the status is the one stored on the version")
		assert.EqualValues(t, 1, u.ModuleVersionNumber)
	}
	require.NotNil(t, body.Usages[0].VersionNumber)
	assert.EqualValues(t, 1, *body.Usages[0].VersionNumber)
	assert.Nil(t, body.Usages[1].VersionNumber, "a draft has no number")

	t.Run("the repository returns the same statuses", func(t *testing.T) {
		usages, err := postgres.NewWorkflowVersionRepo(tenantPool).ListModuleUsages(ctx, tenantID, module.ID)

		require.NoError(t, err)
		require.Len(t, usages, 3)
		got := make([]domain.VersionStatus, len(usages))
		for i, u := range usages {
			got[i] = u.Status
		}
		assert.Equal(t, []domain.VersionStatus{domain.VersionStatusPublished, domain.VersionStatusDraft, domain.VersionStatusArchived}, got)
	})
}

type w10bConcurrentlyChanged struct {
	port.ModuleVersionRepository
	once  sync.Once
	after func()
}

func (r *w10bConcurrentlyChanged) GetByID(ctx context.Context, callerTenantID, id uuid.UUID) (*domain.ModuleVersion, error) {
	v, err := r.ModuleVersionRepository.GetByID(ctx, callerTenantID, id)
	if err == nil {
		r.once.Do(r.after)
	}
	return v, err
}

func TestPublishModuleVersion_HTTP_TheDraftIsPublishedByAnotherRequestAfterTheServiceReadIt_Is409AndTheOtherPublishStands(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	systemPool := fixtures.NewRestrictedRolePool(t, dsn, true, nil, nil)
	tenantID, userID := uuid.New(), uuid.New()
	tenantPool := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantID, &userID)
	plain := w10bModuleRouter(tenantPool, systemPool, nil, nil)
	moduleID, draft := w9cCreateModule(t, plain, tenantID, userID, "Process_W10bPublishRace")
	racing := w10bModuleRouter(tenantPool, systemPool, nil, func(real port.ModuleVersionRepository) port.ModuleVersionRepository {
		return &w10bConcurrentlyChanged{ModuleVersionRepository: real, after: func() {
			other := w9cPublish(plain, tenantID, userID, moduleID, draft)
			require.Equal(t, http.StatusOK, other.Code, other.Body.String())
		}}
	})

	rec := w9cPublish(racing, tenantID, userID, moduleID, draft)

	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	p := w10bDecodeProblem(t, rec)
	assert.Equal(t, w10bProblemConflict, p.Type)
	assert.Equal(t, http.StatusConflict, p.Status)
	assert.Equal(t, "INVALID_VERSION_STATUS", p.Code)
	assert.Equal(t, domain.ErrVersionNotDraft.Error(), p.Detail)
	assert.Equal(t, w9cPublishPath(moduleID, draft), p.Instance)
	row := w9cReadVersion(t, superPool, draft)
	assert.Equal(t, "PUBLISHED", row.status, "the other request's publish stands")
	require.NotNil(t, row.number)
	assert.EqualValues(t, 1, *row.number, "the number the losing request took inside its transaction is not kept")
	pointer := w9cReadPointer(t, superPool, moduleID)
	require.NotNil(t, pointer)
	assert.Equal(t, draft, *pointer)
	assert.Equal(t, 1, w9cCountVersions(t, superPool, moduleID))
}

func TestArchiveModule_HTTP_TheModuleIsGoneWhenThePointerIsCleared_Is404AndTheVersionStaysPublished(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	systemPool := fixtures.NewRestrictedRolePool(t, dsn, true, nil, nil)
	tenantID, userID := uuid.New(), uuid.New()
	tenantPool := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantID, &userID)
	plain := w10bModuleRouter(tenantPool, systemPool, nil, nil)
	moduleID, version := w9cCreateModule(t, plain, tenantID, userID, "Process_W10bArchiveGone")
	require.Equal(t, http.StatusOK, w9cPublish(plain, tenantID, userID, moduleID, version).Code)
	failing := w10bModuleRouter(tenantPool, systemPool, func(real port.ModuleRepository) port.ModuleRepository {
		return w9cRedirectedPointer{ModuleRepository: real, module: uuid.New(), version: nil}
	}, nil)
	path := "/api/v1/modules/" + moduleID.String()

	rec := w10bCall(failing, http.MethodDelete, path, tenantID, userID, "")

	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	p := w10bDecodeProblem(t, rec)
	assert.Equal(t, w10bProblemNotFound, p.Type)
	assert.Equal(t, http.StatusNotFound, p.Status)
	assert.Equal(t, "NOT_FOUND", p.Code)
	assert.Equal(t, "resource not found", p.Detail)
	assert.Equal(t, path, p.Instance)
	assert.Equal(t, "PUBLISHED", w9cReadVersion(t, superPool, version).status, "the version archive was rolled back with the failed pointer update")
	pointer := w9cReadPointer(t, superPool, moduleID)
	require.NotNil(t, pointer, "the module still has its active version")
	assert.Equal(t, version, *pointer)

	retry := w10bCall(plain, http.MethodDelete, path, tenantID, userID, "")
	require.Equal(t, http.StatusOK, retry.Code, "the module can still be archived once the failure is gone: %s", retry.Body.String())
	assert.Equal(t, "ARCHIVED", w9cReadVersion(t, superPool, version).status)
}

func TestArchiveModule_HTTP_TheActiveVersionIsNotPublished_Is409AndNothingChanges(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	systemPool := fixtures.NewRestrictedRolePool(t, dsn, true, nil, nil)
	tenantID, userID := uuid.New(), uuid.New()
	tenantPool := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantID, &userID)
	router := w10bModuleRouter(tenantPool, systemPool, nil, nil)
	moduleID, version := w9cCreateModule(t, router, tenantID, userID, "Process_W10bArchiveNotPublished")
	require.Equal(t, http.StatusOK, w9cPublish(router, tenantID, userID, moduleID, version).Code)
	require.NoError(t, superPool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		_, err := conn.Exec(ctx, `UPDATE workflow_module_version SET status = 'ARCHIVED' WHERE id = $1`, version)
		return err
	}))
	path := "/api/v1/modules/" + moduleID.String()

	rec := w10bCall(router, http.MethodDelete, path, tenantID, userID, "")

	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	p := w10bDecodeProblem(t, rec)
	assert.Equal(t, w10bProblemConflict, p.Type)
	assert.Equal(t, http.StatusConflict, p.Status)
	assert.Equal(t, "INVALID_VERSION_STATUS", p.Code)
	assert.Equal(t, domain.ErrVersionNotPublished.Error(), p.Detail)
	assert.Equal(t, path, p.Instance)
	assert.Equal(t, "ARCHIVED", w9cReadVersion(t, superPool, version).status)
	pointer := w9cReadPointer(t, superPool, moduleID)
	require.NotNil(t, pointer, "the pointer was not cleared by the refused archive")
	assert.Equal(t, version, *pointer)
}
