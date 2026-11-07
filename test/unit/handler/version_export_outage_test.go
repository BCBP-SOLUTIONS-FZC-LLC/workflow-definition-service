package handler_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestExportBPMN_DatabaseUnavailable_Returns503(t *testing.T) {
	t.Parallel()
	outage := fmt.Errorf("query: %w: connection refused", domain.ErrUpstreamUnavailable)
	stored := &domain.WorkflowVersion{
		ID: testVerID, WorkflowID: testWFID, TenantID: testTenantID,
		Status: domain.VersionStatusPublished, BPMNXML: "<definitions/>",
	}
	cases := []struct {
		name           string
		versionLookup  func(*mocks.MockWorkflowVersionRepository)
		workflowLookup func(*mocks.MockWorkflowRepository)
	}{
		{
			name: "version lookup fails",
			versionLookup: func(r *mocks.MockWorkflowVersionRepository) {
				r.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).Return(nil, outage)
			},
		},
		{
			name: "workflow lookup fails after the version was found",
			versionLookup: func(r *mocks.MockWorkflowVersionRepository) {
				r.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).Return(stored, nil)
			},
			workflowLookup: func(r *mocks.MockWorkflowRepository) {
				r.EXPECT().GetByID(gomock.Any(), testTenantID, testWFID).Return(nil, outage)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			versions := mocks.NewMockWorkflowVersionRepository(ctrl)
			workflows := mocks.NewMockWorkflowRepository(ctrl)
			tc.versionLookup(versions)
			if tc.workflowLookup != nil {
				tc.workflowLookup(workflows)
			}
			svc := service.NewVersionService(service.VersionDeps{Versions: versions, Workflows: workflows})
			h := newHandler(nil, nil, &fakeVersionSvc{export: svc.Export}, nil)

			path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/export"
			w := do(newRouter(h), req(http.MethodGet, path, nil))

			require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, "UPSTREAM_UNAVAILABLE", prob["code"])
			assert.Equal(t, "an upstream dependency is temporarily unavailable", prob["detail"])
			assert.Empty(t, w.Header().Get("Content-Disposition"), "no attachment on a failed export")
			assert.NotContains(t, w.Body.String(), "<definitions/>")
			assert.NotContains(t, w.Body.String(), "connection refused", "driver text stays out of the response")
		})
	}
}
