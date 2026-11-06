package handler_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestGetWorkflow_BadIDAndBadVersionsLimit_Returns400BadRequest(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		query string
	}{
		{"non-integer versions_limit", "?versions_limit=abc"},
		{"out-of-range versions_limit", "?versions_limit=101"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			called := false
			h := newHandler(&fakeWorkflowSvc{
				get: func(context.Context, uuid.UUID, uuid.UUID, int) (*domain.Workflow, []*domain.WorkflowVersion, error) {
					called = true
					return nil, nil, nil
				},
			}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})

			w := do(newRouter(h), req(http.MethodGet, "/api/v1/workflows/not-a-uuid"+tc.query, nil))

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, "BAD_REQUEST", prob["code"])
			assert.NotContains(t, prob, "invalid_params")
			assert.False(t, called)
		})
	}
}

func TestGetWorkflow_VersionsLimitSpelling_UsesTheFirstValidValue(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		query     string
		wantLimit int
	}{
		{"leading plus sign", "?versions_limit=%2B5", 5},
		{"repeated parameter keeps the first", "?versions_limit=2&versions_limit=500", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var gotLimit int
			h := newHandler(&fakeWorkflowSvc{
				get: func(_ context.Context, _, _ uuid.UUID, limit int) (*domain.Workflow, []*domain.WorkflowVersion, error) {
					gotLimit = limit
					versions := make([]*domain.WorkflowVersion, 0, limit)
					for range limit {
						versions = append(versions, newPublishedVersion())
					}
					return newWorkflow(), versions, nil
				},
			}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})

			w := do(newRouter(h), req(http.MethodGet, "/api/v1/workflows/"+testWFID.String()+tc.query, nil))

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			assert.Equal(t, tc.wantLimit, gotLimit)
			body := decodeProblem(t, w)
			assert.Len(t, body["versions"], tc.wantLimit)
		})
	}
}

func TestGetWorkflow_IDSpelling_ResolvesToTheSameWorkflow(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		id   string
	}{
		{"canonical lower case", testWFID.String()},
		{"upper case", strings.ToUpper(testWFID.String())},
		{"without hyphens", strings.ReplaceAll(testWFID.String(), "-", "")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var gotTenant, gotID uuid.UUID
			h := newHandler(&fakeWorkflowSvc{
				get: func(_ context.Context, tenantID, id uuid.UUID, _ int) (*domain.Workflow, []*domain.WorkflowVersion, error) {
					gotTenant = tenantID
					gotID = id
					return newWorkflow(), nil, nil
				},
			}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})

			w := do(newRouter(h), req(http.MethodGet, "/api/v1/workflows/"+tc.id, nil))

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			assert.Equal(t, testTenantID, gotTenant)
			assert.Equal(t, testWFID, gotID)
			assert.Equal(t, testWFID.String(), decodeProblem(t, w)["id"])
		})
	}
}
