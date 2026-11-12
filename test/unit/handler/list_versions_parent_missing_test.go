package handler_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func newWiredVersionListRouter(t *testing.T) (*mocks.MockWorkflowRepository, *mocks.MockWorkflowVersionRepository, *gin.Engine) {
	t.Helper()
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Workflows: wfRepo, Versions: vRepo})
	return wfRepo, vRepo, newRouter(handler.New(handler.Services{Versions: svc}))
}

func TestListVersions_RealService_WorkflowNotFound_Returns404WithoutListing(t *testing.T) {
	wfRepo, _, router := newWiredVersionListRouter(t)
	wfRepo.EXPECT().GetByID(gomock.Any(), testTenantID, testWFID).Return(nil, domain.ErrNotFound)

	w := do(router, req(http.MethodGet, versionListPath(testWFID.String()), nil))

	require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
	prob := decodeProblem(t, w)
	assert.Equal(t, "NOT_FOUND", prob["code"])
	assert.NotContains(t, prob, "versions")
	assert.NotContains(t, prob, "pagination")
}

func TestListModuleVersions_RealService_ModuleNotFound_Returns404WithoutListing(t *testing.T) {
	moduleID := uuid.MustParse("66666666-6666-6666-6666-666666666666")
	ctrl := gomock.NewController(t)
	modules := mocks.NewMockModuleRepository(ctrl)
	versions := mocks.NewMockModuleVersionRepository(ctrl)
	svc := service.NewModuleService(service.ModuleDeps{Modules: modules, Versions: versions})
	router := newRouter(handler.New(handler.Services{Modules: svc}))
	modules.EXPECT().GetByID(gomock.Any(), testTenantID, moduleID).Return(nil, domain.ErrNotFound)

	w := do(router, req(http.MethodGet, fmt.Sprintf("/api/v1/modules/%s/versions", moduleID), nil))

	require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
	prob := decodeProblem(t, w)
	assert.Equal(t, "NOT_FOUND", prob["code"])
	assert.NotContains(t, prob, "versions")
	assert.NotContains(t, prob, "pagination")
}
