package handler_test

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	bpmncompiler "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func newWiredDraftRouter(t *testing.T) (*mocks.MockWorkflowRepository, *mocks.MockWorkflowVersionRepository, *gin.Engine) {
	t.Helper()
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewDraftService(service.DraftDeps{Workflows: wfRepo, Versions: vRepo, Compiler: bpmncompiler.New()})
	return wfRepo, vRepo, newRouter(handler.New(handler.Services{Drafts: svc}))
}

func TestDraftRoutes_RealService_MissingDraft_CodeNamesWhatIsMissing(t *testing.T) {
	routes := []struct {
		name   string
		method string
		body   any
	}{
		{name: "read", method: http.MethodGet},
		{name: "save", method: http.MethodPut, body: map[string]any{"name": "renamed", "record_version": 3}},
		{name: "discard", method: http.MethodDelete},
	}
	cases := []struct {
		name        string
		workflow    *domain.Workflow
		workflowErr error
		wantStatus  int
		wantCode    string
	}{
		{
			name:       "workflow exists without a draft",
			workflow:   &domain.Workflow{ID: testWFID, TenantID: testTenantID},
			wantStatus: http.StatusNotFound,
			wantCode:   "DRAFT_NOT_FOUND",
		},
		{
			name:        "workflow missing or in another tenant",
			workflowErr: domain.ErrNotFound,
			wantStatus:  http.StatusNotFound,
			wantCode:    "NOT_FOUND",
		},
		{
			name:        "workflow read fails",
			workflowErr: domain.ErrUpstreamUnavailable,
			wantStatus:  http.StatusServiceUnavailable,
			wantCode:    "UPSTREAM_UNAVAILABLE",
		},
	}
	for _, rt := range routes {
		for _, tc := range cases {
			t.Run(rt.name+"/"+tc.name, func(t *testing.T) {
				wfRepo, vRepo, router := newWiredDraftRouter(t)
				vRepo.EXPECT().GetDraft(gomock.Any(), testTenantID, testWFID).Return(nil, domain.ErrNotFound)
				wfRepo.EXPECT().GetByID(gomock.Any(), testTenantID, testWFID).Return(tc.workflow, tc.workflowErr)

				w := do(router, adminReq(rt.method, "/api/v1/workflows/"+testWFID.String()+"/draft", rt.body))

				require.Equal(t, tc.wantStatus, w.Code, w.Body.String())
				assert.Equal(t, tc.wantCode, decodeProblem(t, w)["code"])
			})
		}
	}
}
