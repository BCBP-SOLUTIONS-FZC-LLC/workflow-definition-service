package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

func TestListModulesQuery_EmptyFilterValues_AreNotForwardedAsFilters(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		path string
	}{
		{"empty q", "/api/v1/modules?q="},
		{"empty scope", "/api/v1/modules?scope="},
		{"both empty", "/api/v1/modules?scope=&q="},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var got port.ModuleFilter
			called := false
			h := newModuleHandler(&fakeModuleSvc{
				list: func(_ context.Context, _ uuid.UUID, f port.ModuleFilter) ([]*domain.Module, int64, error) {
					called = true
					got = f
					return nil, 0, nil
				},
			})

			w := do(newRouter(h), req(http.MethodGet, tc.path, nil))

			require.Equal(t, http.StatusOK, w.Code)
			require.True(t, called)
			assert.Nil(t, got.Search, "an empty q must not become a search filter")
			assert.Nil(t, got.Scope, "an empty scope must not become a scope filter")
		})
	}
}

func TestListModulesQuery_LongOrUnicodeSearch_IsForwardedVerbatim(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		q    string
	}{
		{"over 256 characters", strings.Repeat("a", 300)},
		{"unicode text", "Ünïcødé 承認 フロー 🚀"},
		{"long unicode text", strings.Repeat("承認", 200)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var got port.ModuleFilter
			h := newModuleHandler(&fakeModuleSvc{
				list: func(_ context.Context, _ uuid.UUID, f port.ModuleFilter) ([]*domain.Module, int64, error) {
					got = f
					return []*domain.Module{{ID: testWFID, Name: tc.q}}, 1, nil
				},
			})

			w := do(newRouter(h), req(http.MethodGet, "/api/v1/modules?q="+url.QueryEscape(tc.q), nil))

			require.Equal(t, http.StatusOK, w.Code)
			require.NotNil(t, got.Search)
			assert.Equal(t, tc.q, *got.Search)
			var resp struct {
				Modules []struct {
					Name string `json:"name"`
				} `json:"modules"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			require.Len(t, resp.Modules, 1)
			assert.Equal(t, tc.q, resp.Modules[0].Name)
		})
	}
}

func TestListModulesQuery_NothingToShow_ReturnsAnEmptyArrayWithTheCount(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		path      string
		total     int64
		wantPage  float64
		wantLimit float64
	}{
		{"no visible modules", "/api/v1/modules", 0, 1, 20},
		{"page beyond the last page", "/api/v1/modules?page=9&limit=5", 3, 9, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var gotTenant uuid.UUID
			var gotFilter port.ModuleFilter
			h := newModuleHandler(&fakeModuleSvc{
				list: func(_ context.Context, tenantID uuid.UUID, f port.ModuleFilter) ([]*domain.Module, int64, error) {
					gotTenant, gotFilter = tenantID, f
					return nil, tc.total, nil
				},
			})

			w := do(newRouter(h), req(http.MethodGet, tc.path, nil))

			require.Equal(t, http.StatusOK, w.Code)
			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			modules, isArray := resp["modules"].([]any)
			require.True(t, isArray, "modules must be a JSON array, not null: %s", w.Body.String())
			assert.Empty(t, modules)
			pg, isObject := resp["pagination"].(map[string]any)
			require.True(t, isObject)
			assert.EqualValues(t, tc.total, pg["total_count"])
			assert.Equal(t, tc.wantPage, pg["page"])
			assert.Equal(t, tc.wantLimit, pg["limit"])
			assert.Equal(t, testTenantID, gotTenant)
			assert.EqualValues(t, tc.wantPage, gotFilter.Page)
			assert.EqualValues(t, tc.wantLimit, gotFilter.Limit)
		})
	}
}

func TestListModulesQuery_StorageFailure_IsAnInternalProblemThatHidesTheCause(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
	}{
		{"data query fails", fmt.Errorf("list modules: %w", errors.New("connection reset by peer"))},
		{"row scan fails", fmt.Errorf("list modules scan: %w", errors.New("cannot scan into *time.Time"))},
		{"row iteration fails", fmt.Errorf("list modules rows: %w", errors.New("unexpected EOF"))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newModuleHandler(&fakeModuleSvc{
				list: func(context.Context, uuid.UUID, port.ModuleFilter) ([]*domain.Module, int64, error) {
					return nil, 0, tc.err
				},
			})

			w := do(newRouter(h), req(http.MethodGet, "/api/v1/modules", nil))

			require.Equal(t, http.StatusInternalServerError, w.Code)
			prob := decodeProblem(t, w)
			assert.Equal(t, "INTERNAL_ERROR", prob["code"])
			assert.NotContains(t, w.Body.String(), "connection reset")
			assert.NotContains(t, w.Body.String(), "cannot scan")
			assert.NotContains(t, w.Body.String(), "unexpected EOF")
		})
	}
}
