package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

type starterListCapture struct {
	called bool
	tenant uuid.UUID
	filter port.StarterFilter
}

func newStarterListHandlerCapturing(
	capture *starterListCapture,
	starters []*domain.StarterTemplate,
	total int64,
) *fakeStarterSvc {
	return &fakeStarterSvc{
		list: func(_ context.Context, tenantID uuid.UUID, f port.StarterFilter) ([]*domain.StarterTemplate, int64, error) {
			capture.called = true
			capture.tenant = tenantID
			capture.filter = f
			return starters, total, nil
		},
	}
}

func TestListStartersQuery_EmptyFilterValues_AreNotForwardedAsFilters(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		path string
	}{
		{"empty scope", "/api/v1/starters?scope="},
		{"empty category", "/api/v1/starters?category="},
		{"empty q", "/api/v1/starters?q="},
		{"all three empty", "/api/v1/starters?scope=&category=&q="},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var capture starterListCapture
			h := newStarterHandler(newStarterListHandlerCapturing(&capture, nil, 0))

			w := do(newRouter(h), req(http.MethodGet, tc.path, nil))

			require.Equal(t, http.StatusOK, w.Code)
			require.True(t, capture.called)
			assert.Nil(t, capture.filter.Scope, "an empty scope must not become a scope filter")
			assert.Nil(t, capture.filter.Category, "an empty category must not become a category filter")
			assert.Nil(t, capture.filter.Search, "an empty q must not become a search filter")
		})
	}
}

func TestListStartersQuery_WhitespaceOnlySearch_IsForwardedUntrimmed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		path     string
		wantQ    string
		wantCat  string
		wantCatP bool
	}{
		{"single space in q", "/api/v1/starters?q=%20", " ", "", false},
		{"two spaces in q", "/api/v1/starters?q=%20%20", "  ", "", false},
		{"single space in category", "/api/v1/starters?category=%20", "", " ", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var capture starterListCapture
			h := newStarterHandler(newStarterListHandlerCapturing(&capture, nil, 0))

			w := do(newRouter(h), req(http.MethodGet, tc.path, nil))

			require.Equal(t, http.StatusOK, w.Code)
			if tc.wantCatP {
				require.NotNil(t, capture.filter.Category)
				assert.Equal(t, tc.wantCat, *capture.filter.Category)
				assert.Nil(t, capture.filter.Search)
				return
			}
			require.NotNil(t, capture.filter.Search)
			assert.Equal(t, tc.wantQ, *capture.filter.Search)
			assert.Nil(t, capture.filter.Category)
		})
	}
}

func TestListStartersQuery_RepeatedPagingParams_UseTheFirstValue(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		path      string
		wantPage  float64
		wantLimit float64
	}{
		{"both repeated", "/api/v1/starters?page=1&page=2&limit=5&limit=10", 1, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var capture starterListCapture
			h := newStarterHandler(newStarterListHandlerCapturing(&capture, nil, 12))

			w := do(newRouter(h), req(http.MethodGet, tc.path, nil))

			require.Equal(t, http.StatusOK, w.Code)
			assert.EqualValues(t, tc.wantPage, capture.filter.Page)
			assert.EqualValues(t, tc.wantLimit, capture.filter.Limit)
			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			pg, isObject := resp["pagination"].(map[string]any)
			require.True(t, isObject)
			assert.Equal(t, tc.wantPage, pg["page"])
			assert.Equal(t, tc.wantLimit, pg["limit"])
		})
	}
}

func TestListStartersQuery_BadPaging_Returns400NamingTheParameter(t *testing.T) {
	t.Parallel()

	const (
		pageReason  = "must be an integer between 1 and 100000"
		limitReason = "must be an integer between 1 and 100"
	)
	cases := []struct {
		name string
		path string
		want [][2]string
	}{
		{"page zero", "/api/v1/starters?page=0", [][2]string{{"page", pageReason}}},
		{"page negative", "/api/v1/starters?page=-1", [][2]string{{"page", pageReason}}},
		{"page not a number", "/api/v1/starters?page=abc", [][2]string{{"page", pageReason}}},
		{"page empty", "/api/v1/starters?page=", [][2]string{{"page", pageReason}}},
		{"page above the maximum", "/api/v1/starters?page=100001", [][2]string{{"page", pageReason}}},
		{"limit zero", "/api/v1/starters?limit=0", [][2]string{{"limit", limitReason}}},
		{"limit not a number", "/api/v1/starters?limit=abc", [][2]string{{"limit", limitReason}}},
		{"limit above the maximum", "/api/v1/starters?limit=101", [][2]string{{"limit", limitReason}}},
		{
			"page and limit both bad", "/api/v1/starters?page=0&limit=101",
			[][2]string{{"page", pageReason}, {"limit", limitReason}},
		},
		{
			"bad page is reported before a bad scope", "/api/v1/starters?page=0&scope=bad",
			[][2]string{{"page", pageReason}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var capture starterListCapture
			h := newStarterHandler(newStarterListHandlerCapturing(&capture, nil, 0))

			w := do(newRouter(h), req(http.MethodGet, tc.path, nil))

			require.Equal(t, http.StatusBadRequest, w.Code)
			prob := decodeProblem(t, w)
			assert.Equal(t, "INVALID_INPUT", prob["code"])
			raw, _ := prob["invalid_params"].([]any)
			require.Len(t, raw, len(tc.want), "invalid_params: %v", prob["invalid_params"])
			for i, want := range tc.want {
				entry, ok := raw[i].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, want[0], entry["name"])
				assert.Equal(t, want[1], entry["reason"])
			}
			assert.False(t, capture.called, "a rejected request must not reach the service")
		})
	}
}

func TestListStartersQuery_LargestAcceptedPaging_IsForwardedAndEchoed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		path      string
		total     int64
		wantPage  float64
		wantLimit float64
	}{
		{"past the last page", "/api/v1/starters?page=100000&limit=100", 7, 100000, 100},
		{"second page of five", "/api/v1/starters?page=2&limit=5", 12, 2, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var capture starterListCapture
			h := newStarterHandler(newStarterListHandlerCapturing(&capture, nil, tc.total))

			w := do(newRouter(h), req(http.MethodGet, tc.path, nil))

			require.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, testTenantID, capture.tenant)
			assert.EqualValues(t, tc.wantPage, capture.filter.Page)
			assert.EqualValues(t, tc.wantLimit, capture.filter.Limit)
			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			starters, isArray := resp["starters"].([]any)
			require.True(t, isArray, "starters must be a JSON array, not null: %s", w.Body.String())
			assert.Empty(t, starters)
			pg, isObject := resp["pagination"].(map[string]any)
			require.True(t, isObject)
			assert.EqualValues(t, tc.total, pg["total_count"])
			assert.Equal(t, tc.wantPage, pg["page"])
			assert.Equal(t, tc.wantLimit, pg["limit"])
		})
	}
}

func TestListStartersQuery_NothingVisible_ReturnsAnEmptyArrayAndZeroCount(t *testing.T) {
	t.Parallel()

	var capture starterListCapture
	h := newStarterHandler(newStarterListHandlerCapturing(&capture, nil, 0))

	w := do(newRouter(h), req(http.MethodGet, "/api/v1/starters", nil))

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	starters, isArray := resp["starters"].([]any)
	require.True(t, isArray, "starters must be a JSON array, not null: %s", w.Body.String())
	assert.Empty(t, starters)
	pg, isObject := resp["pagination"].(map[string]any)
	require.True(t, isObject)
	assert.EqualValues(t, 0, pg["total_count"])
	assert.EqualValues(t, 1, pg["page"])
	assert.EqualValues(t, 20, pg["limit"])
}

func TestListStartersQuery_ScopeFilter_IsForwardedToTheService(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		path      string
		wantScope domain.CatalogScope
	}{
		{"tenant scope", "/api/v1/starters?scope=tenant", domain.ScopeTenant},
		{"global scope", "/api/v1/starters?scope=global", domain.ScopeGlobal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var capture starterListCapture
			h := newStarterHandler(newStarterListHandlerCapturing(&capture, nil, 0))

			w := do(newRouter(h), req(http.MethodGet, tc.path, nil))

			require.Equal(t, http.StatusOK, w.Code)
			require.NotNil(t, capture.filter.Scope)
			assert.Equal(t, tc.wantScope, *capture.filter.Scope)
		})
	}
}

func TestListStartersQuery_DatabaseUnavailable_Returns503(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
	}{
		{"unavailable sentinel", domain.ErrUpstreamUnavailable},
		{"wrapped by the repository", fmt.Errorf("list starters count: %w", domain.ErrUpstreamUnavailable)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newStarterHandler(&fakeStarterSvc{
				list: func(context.Context, uuid.UUID, port.StarterFilter) ([]*domain.StarterTemplate, int64, error) {
					return nil, 0, tc.err
				},
			})

			w := do(newRouter(h), req(http.MethodGet, "/api/v1/starters", nil))

			require.Equal(t, http.StatusServiceUnavailable, w.Code)
			prob := decodeProblem(t, w)
			assert.Equal(t, "UPSTREAM_UNAVAILABLE", prob["code"])
		})
	}
}
