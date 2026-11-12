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
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

type w6cListCall struct {
	calls  int
	tenant uuid.UUID
	filter port.WorkflowFilter
}

type w6cListBody struct {
	Workflows  json.RawMessage `json:"workflows"`
	Pagination struct {
		TotalCount *int64 `json:"total_count"`
		Page       *int   `json:"page"`
		Limit      *int   `json:"limit"`
	} `json:"pagination"`
}

func w6cListHandler(rec *w6cListCall, workflows []*domain.Workflow, total int64) *fakeWorkflowSvc {
	return &fakeWorkflowSvc{
		list: func(_ context.Context, tenantID uuid.UUID, f port.WorkflowFilter) ([]*domain.Workflow, int64, error) {
			rec.calls++
			rec.tenant = tenantID
			rec.filter = f
			return workflows, total, nil
		},
	}
}

func w6cDoList(t *testing.T, wf *fakeWorkflowSvc, query string) (*w6cListBody, string) {
	t.Helper()
	h := newHandler(wf, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	w := do(newRouter(h), req(http.MethodGet, "/api/v1/workflows"+query, nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var body w6cListBody
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return &body, w.Body.String()
}

func w6cWorkflows(n int) []*domain.Workflow {
	base := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	out := make([]*domain.Workflow, n)
	for i := range out {
		out[i] = &domain.Workflow{
			ID:          uuid.New(),
			TenantID:    testTenantID,
			TemplateKey: "w6c-key-" + string(rune('a'+i)),
			Name:        "W6c name " + string(rune('a'+i)),
			Description: "W6c description " + string(rune('a'+i)),
			CreatedAt:   base.Add(time.Duration(-i) * time.Hour),
			UpdatedAt:   base.Add(time.Duration(-i)*time.Hour + time.Minute),
		}
	}
	return out
}

func TestListWorkflows_NoQuery_ReturnsEveryWorkflowWithItsFieldsInTheOrderGiven(t *testing.T) {
	t.Parallel()

	workflows := w6cWorkflows(3)
	activeID := uuid.New()
	activeNumber := int32(4)
	workflows[1].ActiveVersionID = &activeID
	workflows[1].ActiveVersionNumber = &activeNumber
	workflows[2].HasDraft = true
	rec := &w6cListCall{}

	h := newHandler(w6cListHandler(rec, workflows, 3), &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	w := do(newRouter(h), req(http.MethodGet, "/api/v1/workflows", nil))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var body struct {
		Workflows  []map[string]any `json:"workflows"`
		Pagination map[string]any   `json:"pagination"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Workflows, 3)
	for i, item := range body.Workflows {
		assert.Equal(t, workflows[i].ID.String(), item["id"], "item %d keeps the position the service gave it", i)
		assert.Equal(t, workflows[i].TemplateKey, item["key"])
		assert.Equal(t, workflows[i].Name, item["name"])
		assert.Equal(t, workflows[i].Description, item["description"])
		createdAt, err := time.Parse(time.RFC3339Nano, item["created_at"].(string))
		require.NoError(t, err)
		assert.True(t, workflows[i].CreatedAt.Equal(createdAt), "created_at of item %d", i)
		updatedAt, err := time.Parse(time.RFC3339Nano, item["updated_at"].(string))
		require.NoError(t, err)
		assert.True(t, workflows[i].UpdatedAt.Equal(updatedAt), "updated_at of item %d", i)
	}
	assert.NotContains(t, body.Workflows[0], "active_version_id")
	assert.NotContains(t, body.Workflows[0], "active_version_number")
	assert.Equal(t, false, body.Workflows[0]["has_draft"])
	assert.Equal(t, activeID.String(), body.Workflows[1]["active_version_id"])
	assert.Equal(t, float64(activeNumber), body.Workflows[1]["active_version_number"])
	assert.Equal(t, true, body.Workflows[2]["has_draft"])
	assert.Equal(t, map[string]any{"total_count": float64(3), "page": float64(1), "limit": float64(20)}, body.Pagination)

	assert.Equal(t, 1, rec.calls)
	assert.Equal(t, testTenantID, rec.tenant)
	assert.Equal(t, 1, rec.filter.Page)
	assert.Equal(t, 20, rec.filter.Limit)
}

func TestListWorkflows_TenantWithNoWorkflows_ReturnsAnEmptyArrayNotNull(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		workflows []*domain.Workflow
	}{
		{name: "service returns a nil list", workflows: nil},
		{name: "service returns an empty list", workflows: []*domain.Workflow{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := &w6cListCall{}

			body, raw := w6cDoList(t, w6cListHandler(rec, tc.workflows, 0), "")

			assert.JSONEq(t, `[]`, string(body.Workflows), raw)
			require.NotNil(t, body.Pagination.TotalCount)
			assert.Equal(t, int64(0), *body.Pagination.TotalCount)
			require.NotNil(t, body.Pagination.Page)
			assert.Equal(t, 1, *body.Pagination.Page)
			require.NotNil(t, body.Pagination.Limit)
			assert.Equal(t, 20, *body.Pagination.Limit)
			assert.Equal(t, testTenantID, rec.tenant)
		})
	}
}

func TestListWorkflows_LargestPageAndLimit_EchoPagingAndKeepTheTotalWithAnEmptyList(t *testing.T) {
	t.Parallel()
	rec := &w6cListCall{}

	body, raw := w6cDoList(t, w6cListHandler(rec, []*domain.Workflow{}, 7), "?page=100000&limit=100")

	assert.JSONEq(t, `[]`, string(body.Workflows), raw)
	require.NotNil(t, body.Pagination.TotalCount)
	assert.Equal(t, int64(7), *body.Pagination.TotalCount)
	require.NotNil(t, body.Pagination.Page)
	assert.Equal(t, 100000, *body.Pagination.Page)
	require.NotNil(t, body.Pagination.Limit)
	assert.Equal(t, 100, *body.Pagination.Limit)
	assert.Equal(t, 1, rec.calls)
	assert.Equal(t, testTenantID, rec.tenant)
	assert.Equal(t, 100000, rec.filter.Page)
	assert.Equal(t, 100, rec.filter.Limit)
}

func TestListWorkflows_LimitOne_PassesThePageSizeAndEchoesItWithTheFullTotal(t *testing.T) {
	t.Parallel()
	all := w6cWorkflows(3)
	var seenLimit int
	wf := &fakeWorkflowSvc{
		list: func(_ context.Context, _ uuid.UUID, f port.WorkflowFilter) ([]*domain.Workflow, int64, error) {
			seenLimit = f.Limit
			return all[:f.Limit], int64(len(all)), nil
		},
	}

	h := newHandler(wf, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	w := do(newRouter(h), req(http.MethodGet, "/api/v1/workflows?page=1&limit=1", nil))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var body struct {
		Workflows  []map[string]any `json:"workflows"`
		Pagination map[string]any   `json:"pagination"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, 1, seenLimit)
	require.Len(t, body.Workflows, 1)
	assert.Equal(t, all[0].ID.String(), body.Workflows[0]["id"])
	assert.Equal(t, map[string]any{"total_count": float64(3), "page": float64(1), "limit": float64(1)}, body.Pagination)
}

func TestListWorkflows_PageAndLimitBothZero_ReportsPageThenLimitWhateverTheQueryOrder(t *testing.T) {
	t.Parallel()

	for _, query := range []string{"?page=0&limit=0", "?limit=0&page=0"} {
		t.Run(query, func(t *testing.T) {
			t.Parallel()
			rec := &w6cListCall{}
			h := newHandler(w6cListHandler(rec, nil, 0), &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})

			w := do(newRouter(h), req(http.MethodGet, "/api/v1/workflows"+query, nil))

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			var prob struct {
				Code          string `json:"code"`
				Detail        string `json:"detail"`
				InvalidParams []struct {
					Name   string `json:"name"`
					Reason string `json:"reason"`
				} `json:"invalid_params"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &prob))
			assert.Equal(t, "INVALID_INPUT", prob.Code)
			assert.Equal(t, "query parameters are invalid", prob.Detail)
			require.Len(t, prob.InvalidParams, 2)
			assert.Equal(t, "page", prob.InvalidParams[0].Name)
			assert.Equal(t, "must be an integer between 1 and 100000", prob.InvalidParams[0].Reason)
			assert.Equal(t, "limit", prob.InvalidParams[1].Name)
			assert.Equal(t, "must be an integer between 1 and 100", prob.InvalidParams[1].Reason)
			assert.Zero(t, rec.calls, "a refused query must not reach the service")
			assert.NotContains(t, w.Body.String(), `"workflows"`)
		})
	}
}
