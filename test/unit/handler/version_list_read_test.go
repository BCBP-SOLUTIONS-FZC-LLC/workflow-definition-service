package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func versionListPath(workflowID string) string {
	return "/api/v1/workflows/" + workflowID + "/versions"
}

func TestListVersions_EmptyQueryValue_Returns400InvalidInput(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		query     string
		wantParam string
	}{
		{name: "page without a value", query: "?page=", wantParam: "page"},
		{name: "limit without a value", query: "?limit=", wantParam: "limit"},
		{name: "valid limit does not rescue an empty page", query: "?page=&limit=5", wantParam: "page"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			listed := false
			h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
				list: func(context.Context, uuid.UUID, uuid.UUID, int, int) ([]*domain.WorkflowVersion, int64, error) {
					listed = true
					return nil, 0, nil
				},
			}, &fakeValidationSvc{})

			w := do(newRouter(h), req(http.MethodGet, versionListPath(testWFID.String())+tc.query, nil))

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			var resp struct {
				Code          string `json:"code"`
				InvalidParams []struct {
					Name string `json:"name"`
				} `json:"invalid_params"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.Equal(t, "INVALID_INPUT", resp.Code)
			require.Len(t, resp.InvalidParams, 1)
			assert.Equal(t, tc.wantParam, resp.InvalidParams[0].Name)
			assert.False(t, listed, "an invalid query must not reach the service")
		})
	}
}

func TestListVersions_WorkflowIdInAnotherUUIDForm_ListsTheSameVersions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		form func(id uuid.UUID) string
	}{
		{name: "canonical", form: uuid.UUID.String},
		{name: "uppercase hex", form: upperHex},
		{name: "no hyphens", form: noHyphens},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ver := newPublishedVersion()
			var gotTenantID, gotWorkflowID uuid.UUID
			h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
				list: func(_ context.Context, tenantID, workflowID uuid.UUID, _, _ int) ([]*domain.WorkflowVersion, int64, error) {
					gotTenantID, gotWorkflowID = tenantID, workflowID
					return []*domain.WorkflowVersion{ver}, 1, nil
				},
			}, &fakeValidationSvc{})

			w := do(newRouter(h), req(http.MethodGet, versionListPath(tc.form(testWFID)), nil))

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			assert.Equal(t, testTenantID, gotTenantID)
			assert.Equal(t, testWFID, gotWorkflowID)
			var resp struct {
				Versions []struct {
					ID string `json:"id"`
				} `json:"versions"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			require.Len(t, resp.Versions, 1)
			assert.Equal(t, ver.ID.String(), resp.Versions[0].ID)
		})
	}
}

func TestListVersions_RepeatedQueryParameter_FirstValueWins(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		query     string
		wantPage  int
		wantLimit int
	}{
		{name: "page repeated", query: "?page=1&page=2", wantPage: 1, wantLimit: 20},
		{name: "page repeated, larger first", query: "?page=3&page=1", wantPage: 3, wantLimit: 20},
		{name: "limit repeated", query: "?limit=5&limit=7", wantPage: 1, wantLimit: 5},
		{name: "invalid second value is never read", query: "?page=2&page=abc", wantPage: 2, wantLimit: 20},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var gotPage, gotLimit int
			h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
				list: func(_ context.Context, _, _ uuid.UUID, page, limit int) ([]*domain.WorkflowVersion, int64, error) {
					gotPage, gotLimit = page, limit
					return nil, 0, nil
				},
			}, &fakeValidationSvc{})

			w := do(newRouter(h), req(http.MethodGet, versionListPath(testWFID.String())+tc.query, nil))

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			assert.Equal(t, tc.wantPage, gotPage)
			assert.Equal(t, tc.wantLimit, gotLimit)
			var resp struct {
				Pagination struct {
					Page  int `json:"page"`
					Limit int `json:"limit"`
				} `json:"pagination"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.Equal(t, tc.wantPage, resp.Pagination.Page)
			assert.Equal(t, tc.wantLimit, resp.Pagination.Limit)
		})
	}
}

func TestListVersions_RealService_PageBeyondTheLast_ReturnsEmptyArrayWithTotalCount(t *testing.T) {
	cases := []struct {
		name     string
		versions []*domain.WorkflowVersion
	}{
		{name: "repository returns an empty slice", versions: []*domain.WorkflowVersion{}},
		{name: "repository returns a nil slice", versions: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vRepo, router := newWiredVersionRouter(t)
			vRepo.EXPECT().ListByWorkflow(gomock.Any(), testTenantID, testWFID, 99, 20).
				Return(tc.versions, int64(5), nil)

			w := do(router, req(http.MethodGet, versionListPath(testWFID.String())+"?page=99", nil))

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			assert.Contains(t, w.Body.String(), `"versions":[]`, "an empty page is an empty array, not null")
			var resp struct {
				Versions   []any `json:"versions"`
				Pagination struct {
					TotalCount int `json:"total_count"`
					Page       int `json:"page"`
				} `json:"pagination"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.NotNil(t, resp.Versions)
			assert.Empty(t, resp.Versions)
			assert.Equal(t, 5, resp.Pagination.TotalCount)
			assert.Equal(t, 99, resp.Pagination.Page)
		})
	}
}

func TestListVersions_RealService_RepositoryFailure_ReturnsProblem(t *testing.T) {
	cases := []struct {
		name           string
		listErr        error
		wantStatus     int
		wantCode       string
		wantTypeSuffix string
		leaked         string
	}{
		{
			name:           "database unavailable",
			listErr:        fmt.Errorf("%w: connection refused", domain.ErrUpstreamUnavailable),
			wantStatus:     http.StatusServiceUnavailable,
			wantCode:       "UPSTREAM_UNAVAILABLE",
			wantTypeSuffix: "/errors/service-unavailable",
			leaked:         "connection refused",
		},
		{
			name:           "unclassified database error",
			listErr:        errors.New("ERROR: permission denied for table workflow_version"),
			wantStatus:     http.StatusInternalServerError,
			wantCode:       "INTERNAL_ERROR",
			wantTypeSuffix: "/errors/internal-error",
			leaked:         "permission denied",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vRepo, router := newWiredVersionRouter(t)
			vRepo.EXPECT().ListByWorkflow(gomock.Any(), testTenantID, testWFID, 1, 20).
				Return(nil, int64(0), tc.listErr)

			w := do(router, req(http.MethodGet, versionListPath(testWFID.String()), nil))

			require.Equal(t, tc.wantStatus, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, tc.wantCode, prob["code"])
			problemType, isString := prob["type"].(string)
			require.True(t, isString, "type = %v", prob["type"])
			assert.True(t, strings.HasSuffix(problemType, tc.wantTypeSuffix), problemType)
			assert.NotContains(t, w.Body.String(), tc.leaked)
			assert.NotContains(t, prob, "versions")
		})
	}
}
