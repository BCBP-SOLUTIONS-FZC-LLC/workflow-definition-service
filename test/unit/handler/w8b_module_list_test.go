package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

const w8bModulesPath = "/api/v1/modules"

type w8bModuleListRecorder struct {
	mu      sync.Mutex
	tenants []uuid.UUID
	filters []port.ModuleFilter
	modules []*domain.Module
	total   int64
}

func (r *w8bModuleListRecorder) fake() *fakeModuleSvc {
	return &fakeModuleSvc{
		list: func(_ context.Context, tenantID uuid.UUID, f port.ModuleFilter) ([]*domain.Module, int64, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.tenants = append(r.tenants, tenantID)
			r.filters = append(r.filters, f)
			return r.modules, r.total, nil
		},
	}
}

func (r *w8bModuleListRecorder) calls() ([]uuid.UUID, []port.ModuleFilter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]uuid.UUID(nil), r.tenants...), append([]port.ModuleFilter(nil), r.filters...)
}

func TestListModulesW8b_PagingBoundaries_AreAcceptedAndPassedOn(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		query     string
		wantPage  int
		wantLimit int
	}{
		{"smallest page and limit", "?page=1&limit=1", 1, 1},
		{"largest page and limit", "?page=100000&limit=100", 100000, 100},
		{"largest limit alone", "?limit=100", 1, 100},
		{"largest page alone", "?page=100000", 100000, 20},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := &w8bModuleListRecorder{total: 7}
			router := newRouter(newModuleHandler(rec.fake()))

			w := do(router, req(http.MethodGet, w8bModulesPath+tc.query, nil))

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			var resp struct {
				Modules    []json.RawMessage `json:"modules"`
				Pagination map[string]any    `json:"pagination"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.Empty(t, resp.Modules)
			assert.EqualValues(t, tc.wantPage, resp.Pagination["page"])
			assert.EqualValues(t, tc.wantLimit, resp.Pagination["limit"])
			assert.EqualValues(t, 7, resp.Pagination["total_count"])
			tenants, filters := rec.calls()
			require.Len(t, filters, 1)
			assert.Equal(t, testTenantID, tenants[0])
			assert.Equal(t, tc.wantPage, filters[0].Page)
			assert.Equal(t, tc.wantLimit, filters[0].Limit)
		})
	}
}

func TestListModulesW8b_PagingOutOfRange_Returns400NamingTheParameter(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		query      string
		wantParam  string
		wantReason string
	}{
		{"limit zero", "?limit=0", "limit", "must be an integer between 1 and 100"},
		{"limit one over the cap", "?limit=101", "limit", "must be an integer between 1 and 100"},
		{"limit negative", "?limit=-5", "limit", "must be an integer between 1 and 100"},
		{"limit not a number", "?limit=abc", "limit", "must be an integer between 1 and 100"},
		{"limit empty", "?limit=", "limit", "must be an integer between 1 and 100"},
		{"limit with a fraction", "?limit=1.5", "limit", "must be an integer between 1 and 100"},
		{"page zero", "?page=0", "page", "must be an integer between 1 and 100000"},
		{"page one over the cap", "?page=100001", "page", "must be an integer between 1 and 100000"},
		{"page negative", "?page=-3", "page", "must be an integer between 1 and 100000"},
		{"page not a number", "?page=abc", "page", "must be an integer between 1 and 100000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := &w8bModuleListRecorder{}
			router := newRouter(newModuleHandler(rec.fake()))

			w := do(router, req(http.MethodGet, w8bModulesPath+tc.query, nil))

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			assert.Contains(t, w.Header().Get("Content-Type"), "application/problem+json")
			prob := decodeProblem(t, w)
			assert.Equal(t, w8bBadRequestProblemType, prob["type"])
			assert.Equal(t, "INVALID_INPUT", prob["code"])
			assert.EqualValues(t, http.StatusBadRequest, prob["status"])
			assert.Equal(t, w8bModulesPath, prob["instance"])
			assert.Equal(t, "query parameters are invalid", prob["detail"])
			params := w8bInvalidParams(t, prob)
			require.Len(t, params, 1)
			assert.Equal(t, tc.wantParam, params[0]["name"])
			assert.Equal(t, tc.wantReason, params[0]["reason"])
			_, filters := rec.calls()
			assert.Empty(t, filters, "a refused query must not reach the service")
		})
	}
}

func TestListModulesW8b_SeveralBadPagingParameters_AreListedPageFirst(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		query     string
		wantNames []string
	}{
		{"page and limit both zero", "?page=0&limit=0", []string{"page", "limit"}},
		{"limit written before page in the query", "?limit=0&page=0", []string{"page", "limit"}},
		{"page and limit both not numbers", "?page=x&limit=y", []string{"page", "limit"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := &w8bModuleListRecorder{}
			router := newRouter(newModuleHandler(rec.fake()))

			w := do(router, req(http.MethodGet, w8bModulesPath+tc.query, nil))

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, w8bBadRequestProblemType, prob["type"])
			assert.Equal(t, "INVALID_INPUT", prob["code"])
			assert.EqualValues(t, http.StatusBadRequest, prob["status"])
			assert.Equal(t, w8bModulesPath, prob["instance"])
			params := w8bInvalidParams(t, prob)
			require.Len(t, params, len(tc.wantNames))
			for i, name := range tc.wantNames {
				assert.Equal(t, name, params[i]["name"])
			}
			_, filters := rec.calls()
			assert.Empty(t, filters)
		})
	}

}

func TestListModulesW8b_NoQuery_AnswersTheDefaultPageWithTheFullFieldListInServiceOrder(t *testing.T) {
	t.Parallel()

	ownTenant := testTenantID
	activeVersion := uuid.MustParse("66666666-6666-6666-6666-666666666666")
	newest := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)
	oldest := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	createdBy := uuid.New()
	ownID := uuid.MustParse("77777777-7777-7777-7777-777777777777")
	globalID := uuid.MustParse("88888888-8888-8888-8888-888888888888")
	rec := &w8bModuleListRecorder{
		total: 2,
		modules: []*domain.Module{
			{
				ID: ownID, TenantID: &ownTenant, Scope: domain.ScopeTenant, Name: "Own review", Description: "mine",
				ProcessID: "Process_Own", ActiveVersionID: &activeVersion, CreatedByUserID: &createdBy,
				RecordVersion: 4, CreatedAt: newest, UpdatedAt: newest,
			},
			{
				ID: globalID, Scope: domain.ScopeGlobal, Name: "Shared review", Description: "everyone",
				ProcessID: "Process_Global", CreatedByUserID: &createdBy, RecordVersion: 2, CreatedAt: oldest, UpdatedAt: oldest,
			},
		},
	}
	router := newRouter(newModuleHandler(rec.fake()))

	w := do(router, req(http.MethodGet, w8bModulesPath, nil))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp struct {
		Modules    []map[string]any `json:"modules"`
		Pagination map[string]any   `json:"pagination"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Modules, 2)
	assert.Equal(t, []any{ownID.String(), globalID.String()}, []any{resp.Modules[0]["id"], resp.Modules[1]["id"]}, "the order of the service is kept")
	assert.EqualValues(t, 2, resp.Pagination["total_count"])
	assert.EqualValues(t, 1, resp.Pagination["page"])
	assert.EqualValues(t, 20, resp.Pagination["limit"])
	assert.Len(t, resp.Pagination, 3)

	own := resp.Modules[0]
	assert.Equal(t, ownTenant.String(), own["tenant_id"])
	assert.Equal(t, "tenant", own["scope"])
	assert.Equal(t, "Own review", own["name"])
	assert.Equal(t, "mine", own["description"])
	assert.Equal(t, "Process_Own", own["process_id"])
	assert.Equal(t, activeVersion.String(), own["active_version_id"])
	assert.Equal(t, newest.Format(time.RFC3339), own["created_at"])
	assert.Equal(t, newest.Format(time.RFC3339), own["updated_at"])
	assert.Equal(t, []string{"active_version_id", "created_at", "description", "id", "name", "process_id", "scope", "tenant_id", "updated_at"}, w8bSortedKeys(own))

	global := resp.Modules[1]
	assert.Equal(t, "global", global["scope"])
	assert.NotContains(t, global, "tenant_id", "a global module has no tenant")
	assert.NotContains(t, global, "active_version_id")
	assert.NotContains(t, global, "record_version")
	assert.NotContains(t, global, "created_by_user_id")

	tenants, filters := rec.calls()
	require.Len(t, filters, 1)
	assert.Equal(t, testTenantID, tenants[0])
	assert.Equal(t, 1, filters[0].Page)
	assert.Equal(t, 20, filters[0].Limit)
	assert.Nil(t, filters[0].Scope)
	assert.Nil(t, filters[0].Search)
}

func TestListModulesW8b_ScopeFilter_TenantAndGlobal_IsPassedToTheService(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		query     string
		wantScope domain.CatalogScope
	}{
		{"scope tenant", "?scope=tenant", domain.ScopeTenant},
		{"scope global", "?scope=global", domain.ScopeGlobal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ownTenant := testTenantID
			rec := &w8bModuleListRecorder{
				total:   1,
				modules: []*domain.Module{{ID: testWFID, TenantID: &ownTenant, Scope: tc.wantScope, Name: "x", ProcessID: "Process_X"}},
			}
			router := newRouter(newModuleHandler(rec.fake()))

			w := do(router, req(http.MethodGet, w8bModulesPath+tc.query, nil))

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			tenants, filters := rec.calls()
			require.Len(t, filters, 1)
			assert.Equal(t, testTenantID, tenants[0])
			require.NotNil(t, filters[0].Scope)
			assert.Equal(t, tc.wantScope, *filters[0].Scope)
			assert.Nil(t, filters[0].Search)
			var resp struct {
				Modules []struct {
					Scope string `json:"scope"`
				} `json:"modules"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			require.Len(t, resp.Modules, 1)
			assert.Equal(t, string(tc.wantScope), resp.Modules[0].Scope)
		})
	}
}

func w8bSortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
