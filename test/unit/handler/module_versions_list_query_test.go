package handler_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func moduleVersionsListPath(query string) string {
	path := "/api/v1/modules/" + testWFID.String() + "/versions"
	if query != "" {
		path += "?" + query
	}
	return path
}

func moduleVersionsInvalidParamNames(t *testing.T, prob map[string]any) []string {
	t.Helper()
	raw, _ := prob["invalid_params"].([]any)
	names := make([]string, 0, len(raw))
	for _, item := range raw {
		entry, ok := item.(map[string]any)
		require.True(t, ok, "invalid_params entry must be an object: %v", item)
		name, _ := entry["name"].(string)
		names = append(names, name)
	}
	return names
}

func TestListModuleVersionsQuery_InvalidModuleID_Returns400(t *testing.T) {
	t.Parallel()

	called := false
	h := newModuleHandler(&fakeModuleSvc{
		listVersions: func(context.Context, uuid.UUID, uuid.UUID, int, int) ([]*domain.ModuleVersion, int64, error) {
			called = true
			return nil, 0, nil
		},
	})

	w := do(newRouter(h), req(http.MethodGet, "/api/v1/modules/not-a-uuid/versions", nil))

	require.Equal(t, http.StatusBadRequest, w.Code)
	prob := decodeProblem(t, w)
	assert.Equal(t, "BAD_REQUEST", prob["code"])
	assert.Equal(t, "invalid id: not a valid UUID", prob["detail"])
	assert.False(t, called, "a malformed id must not reach the service")
}

func TestListModuleVersionsQuery_InvalidPagination_Returns400NamingTheParameters(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		query     string
		wantNames []string
	}{
		{"page zero", "page=0", []string{"page"}},
		{"page negative", "page=-1", []string{"page"}},
		{"page not a number", "page=abc", []string{"page"}},
		{"page above the maximum", "page=100001", []string{"page"}},
		{"limit zero", "limit=0", []string{"limit"}},
		{"limit above the maximum", "limit=101", []string{"limit"}},
		{"limit not a number", "limit=abc", []string{"limit"}},
		{"page and limit both invalid", "page=0&limit=500", []string{"page", "limit"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			called := false
			h := newModuleHandler(&fakeModuleSvc{
				listVersions: func(context.Context, uuid.UUID, uuid.UUID, int, int) ([]*domain.ModuleVersion, int64, error) {
					called = true
					return nil, 0, nil
				},
			})

			w := do(newRouter(h), req(http.MethodGet, moduleVersionsListPath(tc.query), nil))

			require.Equal(t, http.StatusBadRequest, w.Code)
			prob := decodeProblem(t, w)
			assert.Equal(t, "INVALID_INPUT", prob["code"])
			assert.Equal(t, tc.wantNames, moduleVersionsInvalidParamNames(t, prob))
			assert.False(t, called, "invalid pagination must not reach the service")
		})
	}
}

func TestListModuleVersionsQuery_PaginationBounds_AreForwardedAndEchoed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		query     string
		wantPage  int
		wantLimit int
	}{
		{"defaults", "", 1, 20},
		{"upper bounds", "page=100000&limit=100", 100000, 100},
		{"lower bounds", "page=1&limit=1", 1, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var gotPage, gotLimit int
			var gotTenant, gotModule uuid.UUID
			h := newModuleHandler(&fakeModuleSvc{
				listVersions: func(_ context.Context, tenantID, moduleID uuid.UUID, page, limit int) ([]*domain.ModuleVersion, int64, error) {
					gotTenant, gotModule = tenantID, moduleID
					gotPage, gotLimit = page, limit
					return []*domain.ModuleVersion{{ID: testVerID}}, 7, nil
				},
			})

			w := do(newRouter(h), req(http.MethodGet, moduleVersionsListPath(tc.query), nil))

			require.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, tc.wantPage, gotPage)
			assert.Equal(t, tc.wantLimit, gotLimit)
			assert.Equal(t, testTenantID, gotTenant)
			assert.Equal(t, testWFID, gotModule)
			body := decodeProblem(t, w)
			pagination, ok := body["pagination"].(map[string]any)
			require.True(t, ok, "body: %s", w.Body.String())
			assert.EqualValues(t, tc.wantPage, pagination["page"])
			assert.EqualValues(t, tc.wantLimit, pagination["limit"])
			assert.EqualValues(t, 7, pagination["total_count"])
		})
	}
}

func TestListModuleVersionsQuery_PageBeyondLastPage_ReturnsEmptyListWithTotal(t *testing.T) {
	t.Parallel()

	h := newModuleHandler(&fakeModuleSvc{
		listVersions: func(_ context.Context, _, _ uuid.UUID, page, _ int) ([]*domain.ModuleVersion, int64, error) {
			require.Equal(t, 99, page)
			return []*domain.ModuleVersion{}, 3, nil
		},
	})

	w := do(newRouter(h), req(http.MethodGet, moduleVersionsListPath("page=99"), nil))

	require.Equal(t, http.StatusOK, w.Code)
	body := decodeProblem(t, w)
	versions, ok := body["versions"].([]any)
	require.True(t, ok, "versions must be an empty array, not null: %s", w.Body.String())
	assert.Empty(t, versions)
	pagination, ok := body["pagination"].(map[string]any)
	require.True(t, ok)
	assert.EqualValues(t, 3, pagination["total_count"])
	assert.EqualValues(t, 99, pagination["page"])
}

func TestListModuleVersionsQuery_StoreFailure_MapsToStatusAndCode(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"database unreachable", domain.ErrUpstreamUnavailable, http.StatusServiceUnavailable, "UPSTREAM_UNAVAILABLE"},
		{
			"unavailable wrapped by the service",
			fmt.Errorf("list module versions: %w", domain.ErrUpstreamUnavailable),
			http.StatusServiceUnavailable, "UPSTREAM_UNAVAILABLE",
		},
		{"any other database error", errors.New("list query failed"), http.StatusInternalServerError, "INTERNAL_ERROR"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newModuleHandler(&fakeModuleSvc{
				listVersions: func(context.Context, uuid.UUID, uuid.UUID, int, int) ([]*domain.ModuleVersion, int64, error) {
					return nil, 0, tc.err
				},
			})

			w := do(newRouter(h), req(http.MethodGet, moduleVersionsListPath(""), nil))

			require.Equal(t, tc.wantStatus, w.Code)
			prob := decodeProblem(t, w)
			assert.Equal(t, tc.wantCode, prob["code"])
			assert.NotContains(t, w.Body.String(), "list query failed", "raw database text must not leak")
		})
	}
}
