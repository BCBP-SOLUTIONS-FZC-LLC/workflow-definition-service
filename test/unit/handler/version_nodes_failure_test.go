package handler_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func nodesRouterOverStoredVersion(t *testing.T, v *domain.WorkflowVersion, getErr error) *gin.Engine {
	t.Helper()
	repo := mocks.NewMockWorkflowVersionRepository(gomock.NewController(t))
	repo.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).Return(v, getErr)
	svc := service.NewVersionService(service.VersionDeps{Versions: repo, Compiler: bpmn_compiler.New()})
	return newRouter(newHandler(nil, nil, &fakeVersionSvc{nodes: svc.Nodes}, nil))
}

func TestGetVersionNodes_StoredVersionCannotBeRead_ReturnsProblem(t *testing.T) {
	t.Parallel()
	notJSON := "{not json"
	cases := []struct {
		name       string
		version    *domain.WorkflowVersion
		getErr     error
		wantStatus int
		wantCode   string
		wantDetail string
	}{
		{
			name:       "stored plan is not valid JSON",
			version:    &domain.WorkflowVersion{ID: testVerID, WorkflowID: testWFID, TenantID: testTenantID, Status: domain.VersionStatusPublished, CompiledPlanJSON: &notJSON},
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL_ERROR",
			wantDetail: "an unexpected error occurred",
		},
		{
			name:       "database unreachable",
			getErr:     fmt.Errorf("get version: %w", domain.ErrUpstreamUnavailable),
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "UPSTREAM_UNAVAILABLE",
			wantDetail: "an upstream dependency is temporarily unavailable",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := do(nodesRouterOverStoredVersion(t, tc.version, tc.getErr), req(http.MethodGet, nodesPath, nil))
			require.Equal(t, tc.wantStatus, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, tc.wantCode, prob["code"])
			assert.Equal(t, tc.wantDetail, prob["detail"])
		})
	}
}

func TestGetVersionNodes_WorkflowIDIsNotAUUID_Returns400(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		path string
		want string
	}{
		{"workflow id", "/api/v1/workflows/not-a-uuid/versions/" + testVerID.String() + "/nodes", "invalid id: not a valid UUID"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			called := false
			vs := &fakeVersionSvc{nodes: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*service.VersionNodes, error) {
				called = true
				return nil, nil
			}}
			w := do(newRouter(newHandler(nil, nil, vs, nil)), req(http.MethodGet, tc.path, nil))
			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, "BAD_REQUEST", prob["code"])
			assert.Equal(t, tc.want, prob["detail"])
			assert.False(t, called, "the service must not be reached with a malformed id")
		})
	}
}
