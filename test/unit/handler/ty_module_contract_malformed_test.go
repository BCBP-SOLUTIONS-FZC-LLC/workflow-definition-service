package handler_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestGetModuleContract_StoredXMLCannotBeParsed_Returns400WithoutTheParserText(t *testing.T) {
	t.Parallel()

	const parserText = "XML syntax error on line 3: unexpected EOF"
	ctrl := gomock.NewController(t)
	versions := mocks.NewMockModuleVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewModuleService(service.ModuleDeps{
		Versions:       versions,
		GlobalVersions: mocks.NewMockModuleVersionRepository(ctrl),
		Compiler:       compiler,
	})
	one := int32(1)
	versions.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).Return(&domain.ModuleVersion{
		ID: testVerID, ModuleID: testWFID, TenantID: &testTenantID, Scope: domain.ScopeTenant,
		ProcessID: "Process_Damaged", VersionNumber: &one, Status: domain.VersionStatusPublished, BPMNXML: "<damaged",
	}, nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<damaged").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<damaged", gomock.Any()).Return("<damaged", nil)
	compiler.EXPECT().CompileModule(gomock.Any(), "<damaged", "Process_Damaged").
		Return(nil, domain.ModuleDeclarations{}, fmt.Errorf("%w: %s", domain.ErrMalformedBPMN, parserText))
	router := newRouter(handler.New(handler.Services{Modules: svc}))

	w := do(router, req(http.MethodGet,
		"/api/v1/modules/"+testWFID.String()+"/versions/"+testVerID.String()+"/contract", nil))

	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	prob := decodeProblem(t, w)
	assert.Equal(t, "INVALID_BPMN_XML", prob["code"])
	assert.Equal(t, "the BPMN document could not be parsed", prob["detail"])
	assert.NotContains(t, w.Body.String(), parserText)
	assert.NotContains(t, prob, "invalid_params")
}
