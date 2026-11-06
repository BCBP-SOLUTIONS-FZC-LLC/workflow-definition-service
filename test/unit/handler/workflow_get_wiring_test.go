package handler_test

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func newWiredWorkflowRouter(t *testing.T) (
	*mocks.MockWorkflowRepository, *mocks.MockWorkflowVersionRepository, *gin.Engine,
) {
	t.Helper()
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewWorkflowService(service.WorkflowDeps{Workflows: wfRepo, Versions: vRepo})
	return wfRepo, vRepo, newRouter(handler.New(handler.Services{Workflows: svc}))
}

func TestGetWorkflow_RealService_RepositoryFailure_ReturnsProblem(t *testing.T) {
	cases := []struct {
		name           string
		lookupErr      error
		listErr        error
		wantStatus     int
		wantCode       string
		wantTypeSuffix string
	}{
		{
			name:           "workflow owned by another tenant",
			lookupErr:      domain.ErrNotFound,
			wantStatus:     http.StatusNotFound,
			wantCode:       "NOT_FOUND",
			wantTypeSuffix: "/errors/not-found",
		},
		{
			name:           "database unavailable on the workflow lookup",
			lookupErr:      fmt.Errorf("%w: connection refused", domain.ErrUpstreamUnavailable),
			wantStatus:     http.StatusServiceUnavailable,
			wantCode:       "UPSTREAM_UNAVAILABLE",
			wantTypeSuffix: "/errors/service-unavailable",
		},
		{
			name:           "unexpected database error on the workflow lookup",
			lookupErr:      errors.New("pq: relation workflow is corrupt"),
			wantStatus:     http.StatusInternalServerError,
			wantCode:       "INTERNAL_ERROR",
			wantTypeSuffix: "/errors/internal-error",
		},
		{
			name:           "database unavailable on the version list",
			listErr:        fmt.Errorf("%w: connection refused", domain.ErrUpstreamUnavailable),
			wantStatus:     http.StatusServiceUnavailable,
			wantCode:       "UPSTREAM_UNAVAILABLE",
			wantTypeSuffix: "/errors/service-unavailable",
		},
		{
			name:           "unexpected database error on the version list",
			listErr:        errors.New("pq: relation workflow_version is corrupt"),
			wantStatus:     http.StatusInternalServerError,
			wantCode:       "INTERNAL_ERROR",
			wantTypeSuffix: "/errors/internal-error",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wfRepo, vRepo, router := newWiredWorkflowRouter(t)
			if tc.lookupErr != nil {
				wfRepo.EXPECT().GetByID(gomock.Any(), testTenantID, testWFID).Return(nil, tc.lookupErr)
			} else {
				wfRepo.EXPECT().GetByID(gomock.Any(), testTenantID, testWFID).Return(newWorkflow(), nil)
				vRepo.EXPECT().ListByWorkflow(gomock.Any(), testTenantID, testWFID, 1, 20).
					Return(nil, int64(0), tc.listErr)
			}

			w := do(router, req(http.MethodGet, "/api/v1/workflows/"+testWFID.String(), nil))

			require.Equal(t, tc.wantStatus, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, tc.wantCode, prob["code"])
			problemType, isString := prob["type"].(string)
			require.True(t, isString, "type = %v", prob["type"])
			assert.True(t, strings.HasSuffix(problemType, tc.wantTypeSuffix), problemType)
			assert.NotContains(t, w.Body.String(), "corrupt")
			assert.NotContains(t, w.Body.String(), "connection refused")
		})
	}
}

func TestGetWorkflow_RealService_ArchivedWorkflow_OmitsActiveVersionAndListsArchivedVersions(t *testing.T) {
	wfRepo, vRepo, router := newWiredWorkflowRouter(t)

	archived := newPublishedVersion()
	archived.Status = domain.VersionStatusArchived
	wf := newWorkflow()
	wfRepo.EXPECT().GetByID(gomock.Any(), testTenantID, testWFID).Return(wf, nil)
	vRepo.EXPECT().ListByWorkflow(gomock.Any(), testTenantID, testWFID, 1, 20).
		Return([]*domain.WorkflowVersion{archived}, int64(1), nil)

	w := do(router, req(http.MethodGet, "/api/v1/workflows/"+testWFID.String(), nil))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := decodeProblem(t, w)
	assert.NotContains(t, body, "active_version_id")
	assert.NotContains(t, body, "active_version_number")
	versions, ok := body["versions"].([]any)
	require.True(t, ok)
	require.Len(t, versions, 1)
	assert.Equal(t, "ARCHIVED", versions[0].(map[string]any)["status"])
	assert.Equal(t, archived.ID.String(), versions[0].(map[string]any)["id"])
}
