package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

type sgDVersionListCall struct {
	tenantID, workflowID uuid.UUID
	page, limit          int
}

func sgDListVersions(t *testing.T, query string, versions []*domain.WorkflowVersion, total int64) (*sgDVersionListCall, int, []byte) {
	t.Helper()
	var call *sgDVersionListCall
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
		list: func(_ context.Context, tenantID, workflowID uuid.UUID, page, limit int) ([]*domain.WorkflowVersion, int64, error) {
			call = &sgDVersionListCall{tenantID: tenantID, workflowID: workflowID, page: page, limit: limit}
			return versions, total, nil
		},
	}, &fakeValidationSvc{})
	w := do(newRouter(h), req(http.MethodGet, versionListPath(testWFID.String())+query, nil))
	return call, w.Code, w.Body.Bytes()
}

func TestListVersions_NoQuery_UsesPageOneLimitTwentyAndEchoesThem(t *testing.T) {
	t.Parallel()

	published := newPublishedVersion()
	publishedAt := time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)
	published.PublishedAt = &publishedAt
	creator := uuid.New()
	published.CreatedByUserID = creator

	call, code, body := sgDListVersions(t, "", []*domain.WorkflowVersion{published}, 41)

	require.Equal(t, http.StatusOK, code, string(body))
	require.NotNil(t, call)
	assert.Equal(t, sgDVersionListCall{tenantID: testTenantID, workflowID: testWFID, page: 1, limit: 20}, *call)
	var resp struct {
		Versions   []map[string]any `json:"versions"`
		Pagination map[string]any   `json:"pagination"`
	}
	require.NoError(t, json.Unmarshal(body, &resp))
	assert.Equal(t, map[string]any{"total_count": float64(41), "page": float64(1), "limit": float64(20)}, resp.Pagination)
	require.Len(t, resp.Versions, 1)
	item := resp.Versions[0]
	assert.Equal(t, published.ID.String(), item["id"])
	assert.Equal(t, "PUBLISHED", item["status"])
	assert.Equal(t, float64(1), item["version_number"])
	assert.Equal(t, "2026-05-06T07:08:09Z", item["published_at"])
	assert.Equal(t, true, item["is_valid"])
	assert.Equal(t, creator.String(), item["created_by_user_id"])
}

func TestListVersions_ExplicitPaging_IsPassedOnAndEchoed(t *testing.T) {
	t.Parallel()

	call, code, body := sgDListVersions(t, "?page=2&limit=1", []*domain.WorkflowVersion{newPublishedVersion()}, 3)

	require.Equal(t, http.StatusOK, code, string(body))
	require.NotNil(t, call)
	assert.Equal(t, sgDVersionListCall{tenantID: testTenantID, workflowID: testWFID, page: 2, limit: 1}, *call)
	var resp struct {
		Pagination map[string]any `json:"pagination"`
	}
	require.NoError(t, json.Unmarshal(body, &resp))
	assert.Equal(t, map[string]any{"total_count": float64(3), "page": float64(2), "limit": float64(1)}, resp.Pagination)
}

func TestListVersions_DraftSummary_OmitsNumberAndPublicationTime(t *testing.T) {
	t.Parallel()

	draft := newDraftVersion()
	invalid := newDraftVersion()
	invalid.ID = uuid.New()
	invalid.IsValid = false

	_, code, body := sgDListVersions(t, "", []*domain.WorkflowVersion{draft, invalid}, 2)

	require.Equal(t, http.StatusOK, code, string(body))
	var resp struct {
		Versions []map[string]any `json:"versions"`
	}
	require.NoError(t, json.Unmarshal(body, &resp))
	require.Len(t, resp.Versions, 2)
	for _, item := range resp.Versions {
		assert.Equal(t, "DRAFT", item["status"])
		assert.NotContains(t, item, "version_number")
		assert.NotContains(t, item, "published_at")
		assert.Contains(t, item, "is_valid")
		assert.NotContains(t, item, "bpmn_xml")
		assert.NotContains(t, item, "record_version")
	}
	assert.Equal(t, true, resp.Versions[0]["is_valid"])
	assert.Equal(t, false, resp.Versions[1]["is_valid"])
	assert.Equal(t, draft.ID.String(), resp.Versions[0]["id"])
	assert.Equal(t, invalid.ID.String(), resp.Versions[1]["id"])
}

func TestListVersions_PagingBoundaries_AcceptedAtTheEdgeAndRefusedBeyondIt(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		query      string
		wantOK     bool
		wantPage   int
		wantLimit  int
		wantParam  string
		wantReason string
	}{
		{name: "limit 100 is the largest accepted", query: "?limit=100", wantOK: true, wantPage: 1, wantLimit: 100},
		{name: "limit 101 is refused", query: "?limit=101", wantParam: "limit", wantReason: "must be an integer between 1 and 100"},
		{name: "page 100000 is the largest accepted", query: "?page=100000", wantOK: true, wantPage: 100000, wantLimit: 20},
		{name: "page 100001 is refused", query: "?page=100001", wantParam: "page", wantReason: "must be an integer between 1 and 100000"},
		{name: "limit 1 is the smallest accepted", query: "?limit=1", wantOK: true, wantPage: 1, wantLimit: 1},
		{name: "limit 0 is refused", query: "?limit=0", wantParam: "limit", wantReason: "must be an integer between 1 and 100"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			call, code, body := sgDListVersions(t, tc.query, nil, 0)

			if tc.wantOK {
				require.Equal(t, http.StatusOK, code, string(body))
				require.NotNil(t, call)
				assert.Equal(t, tc.wantPage, call.page)
				assert.Equal(t, tc.wantLimit, call.limit)
				return
			}
			require.Equal(t, http.StatusBadRequest, code, string(body))
			assert.Nil(t, call, "a refused query must not reach the service")
			var prob struct {
				Code          string `json:"code"`
				InvalidParams []struct {
					Name   string `json:"name"`
					Reason string `json:"reason"`
				} `json:"invalid_params"`
			}
			require.NoError(t, json.Unmarshal(body, &prob))
			assert.Equal(t, "INVALID_INPUT", prob.Code)
			require.Len(t, prob.InvalidParams, 1)
			assert.Equal(t, tc.wantParam, prob.InvalidParams[0].Name)
			assert.Equal(t, tc.wantReason, prob.InvalidParams[0].Reason)
		})
	}
}

func TestListVersions_BothPagingParametersBad_ReportsPageThenLimit(t *testing.T) {
	t.Parallel()

	call, code, body := sgDListVersions(t, "?page=0&limit=500", nil, 0)

	require.Equal(t, http.StatusBadRequest, code, string(body))
	assert.Nil(t, call)
	var prob struct {
		Code          string `json:"code"`
		InvalidParams []struct {
			Name   string `json:"name"`
			Reason string `json:"reason"`
		} `json:"invalid_params"`
	}
	require.NoError(t, json.Unmarshal(body, &prob))
	assert.Equal(t, "INVALID_INPUT", prob.Code)
	require.Len(t, prob.InvalidParams, 2)
	assert.Equal(t, "page", prob.InvalidParams[0].Name)
	assert.Equal(t, "must be an integer between 1 and 100000", prob.InvalidParams[0].Reason)
	assert.Equal(t, "limit", prob.InvalidParams[1].Name)
	assert.Equal(t, "must be an integer between 1 and 100", prob.InvalidParams[1].Reason)
}
