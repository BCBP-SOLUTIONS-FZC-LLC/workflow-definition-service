package handler_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func publishRequestTestPath() string {
	return "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/publish"
}

func TestPublishVersion_TenantOwnerRole_Returns200(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		roles string
	}{
		{name: "tenant_owner alone", roles: "tenant_owner"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var gotTenant, gotWorkflow, gotVersion uuid.UUID
			h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
				publish: func(_ context.Context, tenantID, _, workflowID, versionID uuid.UUID, _ bool) (*domain.WorkflowVersion, error) {
					gotTenant, gotWorkflow, gotVersion = tenantID, workflowID, versionID
					return newPublishedVersion(), nil
				},
			}, &fakeValidationSvc{})

			r := req(http.MethodPost, publishRequestTestPath(), nil)
			r.Header.Set("x-tenant-roles", tc.roles)
			w := do(newRouter(h), r)

			require.Equal(t, http.StatusOK, w.Code)
			body := decodeProblem(t, w)
			assert.Equal(t, "Version published", body["message"])
			assert.Equal(t, "PUBLISHED", body["status"])
			assert.Equal(t, testVerID.String(), body["version_id"])
			assert.Equal(t, testWFID, gotWorkflow)
			assert.Equal(t, testVerID, gotVersion)
			assert.Equal(t, testTenantID, gotTenant)
		})
	}
}

func TestPublishVersion_BodyShape_DecidesTheOverrideFlag(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		body     string
		wantSkip bool
	}{
		{name: "a JSON null body leaves the override off", body: `null`, wantSkip: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var reached, gotSkip bool
			h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
				publish: func(_ context.Context, _, _, _, _ uuid.UUID, skip bool) (*domain.WorkflowVersion, error) {
					reached, gotSkip = true, skip
					return newPublishedVersion(), nil
				},
			}, &fakeValidationSvc{})

			r := adminReq(http.MethodPost, publishRequestTestPath(), nil)
			r.Body = io.NopCloser(strings.NewReader(tc.body))
			w := do(newRouter(h), r)

			require.Equal(t, http.StatusOK, w.Code)
			assert.True(t, reached)
			assert.Equal(t, tc.wantSkip, gotSkip)
			assert.Equal(t, "Version published", decodeProblem(t, w)["message"])
		})
	}
}

func TestPublishVersion_ModuleLookupFailure_MapsByCause(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		err      error
		wantCode int
		wantType string
	}{
		{
			name:     "an unreachable module store is a 503",
			err:      fmt.Errorf("resolve module %q: %w", "Process_Review", fmt.Errorf("%w: connection refused", domain.ErrUpstreamUnavailable)),
			wantCode: http.StatusServiceUnavailable,
			wantType: "UPSTREAM_UNAVAILABLE",
		},
		{
			name:     "any other module store failure is a 500",
			err:      fmt.Errorf("resolve module %q: %w", "Process_Review", errors.New("scan module version: unexpected column type")),
			wantCode: http.StatusInternalServerError,
			wantType: "INTERNAL_ERROR",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
				publish: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID, bool) (*domain.WorkflowVersion, error) {
					return nil, tc.err
				},
			}, &fakeValidationSvc{})

			w := do(newRouter(h), adminReq(http.MethodPost, publishRequestTestPath(), nil))

			require.Equal(t, tc.wantCode, w.Code)
			prob := decodeProblem(t, w)
			assert.Equal(t, tc.wantType, prob["code"])
			assert.NotContains(t, w.Body.String(), "Process_Review", "the cause is logged, not returned")
			assert.NotContains(t, w.Body.String(), "unexpected column")
		})
	}
}
