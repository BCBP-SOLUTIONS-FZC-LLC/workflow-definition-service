package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

func queryPtr[T any](v T) *T { return &v }

func listWithCapturedFilter(t *testing.T, target string) port.WorkflowFilter {
	t.Helper()
	var captured port.WorkflowFilter
	h := newHandler(&fakeWorkflowSvc{
		list: func(_ context.Context, _ uuid.UUID, f port.WorkflowFilter) ([]*domain.Workflow, int64, error) {
			captured = f
			return nil, 0, nil
		},
	}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})

	w := do(newRouter(h), req(http.MethodGet, target, nil))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	return captured
}

func TestListWorkflows_QueryFilters_AreReadIntoTheServiceFilter(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		query        string
		wantSearch   *string
		wantKey      *string
		wantIsValid  *bool
		wantHasDraft *bool
		wantArchived *bool
	}{
		{
			name:  "all filters present but empty leave the list unfiltered",
			query: "?search=&key=&is_valid=&has_draft=&status=",
		},
		{
			name:  "no query parameters leave the list unfiltered",
			query: "",
		},
		{
			name:       "a single space search is passed through untrimmed",
			query:      "?search=%20",
			wantSearch: queryPtr(" "),
		},
		{
			name:        "is_valid false filters for invalid active versions",
			query:       "?is_valid=false",
			wantIsValid: queryPtr(false),
		},
		{
			name:         "status archived filters for archived workflows",
			query:        "?status=archived",
			wantArchived: queryPtr(true),
		},
		{
			name:  "unknown query parameters are ignored",
			query: "?colour=red&sort=name",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := listWithCapturedFilter(t, "/api/v1/workflows"+tc.query)

			assert.Equal(t, tc.wantSearch, got.Search)
			assert.Equal(t, tc.wantKey, got.TemplateKey)
			assert.Equal(t, tc.wantIsValid, got.IsValid)
			assert.Equal(t, tc.wantHasDraft, got.HasDraft)
			assert.Equal(t, tc.wantArchived, got.Archived)
			assert.Equal(t, 1, got.Page)
			assert.Equal(t, 20, got.Limit)
		})
	}
}

func TestListWorkflows_ActiveVersionAndDraft_AreReportedPerWorkflow(t *testing.T) {
	t.Parallel()

	activeID := uuid.New()
	activeNumber := int32(3)
	withBoth := newWorkflow()
	withBoth.ActiveVersionID = &activeID
	withBoth.ActiveVersionNumber = &activeNumber
	withBoth.HasDraft = true

	h := newHandler(&fakeWorkflowSvc{
		list: func(_ context.Context, _ uuid.UUID, _ port.WorkflowFilter) ([]*domain.Workflow, int64, error) {
			return []*domain.Workflow{withBoth}, 1, nil
		},
	}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})

	w := do(newRouter(h), req(http.MethodGet, "/api/v1/workflows", nil))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var body struct {
		Workflows []map[string]any `json:"workflows"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Workflows, 1)
	item := body.Workflows[0]
	assert.Equal(t, activeID.String(), item["active_version_id"])
	assert.Equal(t, float64(activeNumber), item["active_version_number"])
	assert.Equal(t, true, item["has_draft"])
}

func TestListWorkflows_UnexpectedServiceError_Returns500(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
	}{
		{"list fails after the count succeeded", errors.New("list workflows: connection reset")},
		{"row scan fails", errors.New("list workflows scan: cannot scan")},
		{"row iteration fails", errors.New("list workflows rows: network error")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHandler(&fakeWorkflowSvc{
				list: func(_ context.Context, _ uuid.UUID, _ port.WorkflowFilter) ([]*domain.Workflow, int64, error) {
					return nil, 0, tc.err
				},
			}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})

			w := do(newRouter(h), req(http.MethodGet, "/api/v1/workflows", nil))

			require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, "INTERNAL_ERROR", prob["code"])
			assert.Contains(t, prob["type"], "internal-error")
			assert.NotContains(t, prob, "workflows")
			assert.NotContains(t, prob, "pagination")
			assert.NotContains(t, w.Body.String(), tc.err.Error())
		})
	}
}
