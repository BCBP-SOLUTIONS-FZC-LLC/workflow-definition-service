package handler_test

import (
	"errors"
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

func sgCExportHandler(t *testing.T, versions *mocks.MockWorkflowVersionRepository, workflows *mocks.MockWorkflowRepository, log *sgCLog) *handler.Handler {
	t.Helper()
	svc := service.NewVersionService(service.VersionDeps{Versions: versions, Workflows: workflows})
	return handler.New(handler.Services{
		Versions: &fakeVersionSvc{export: svc.Export},
		Log:      log,
	})
}

func sgCExportPath() string {
	return "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/export"
}

func TestExportBPMN_WorkflowLookupFails_ReturnsTheMappedProblemWithoutAnAttachment(t *testing.T) {
	t.Parallel()

	const driverText = "pq: could not serialize access"
	cases := []struct {
		name       string
		lookupErr  error
		wantStatus int
		wantCode   string
		wantDetail string
		wantLogged bool
	}{
		{
			name:       "workflow row missing is not found",
			lookupErr:  domain.ErrNotFound,
			wantStatus: http.StatusNotFound,
			wantCode:   "NOT_FOUND",
		},
		{
			name:       "unexpected failure is internal and not echoed",
			lookupErr:  errors.New(driverText),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL_ERROR",
			wantDetail: "an unexpected error occurred",
			wantLogged: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			versions := mocks.NewMockWorkflowVersionRepository(ctrl)
			workflows := mocks.NewMockWorkflowRepository(ctrl)
			versions.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).Return(
				&domain.WorkflowVersion{ID: testVerID, WorkflowID: testWFID, TenantID: testTenantID, BPMNXML: "<definitions/>"}, nil)
			workflows.EXPECT().GetByID(gomock.Any(), testTenantID, testWFID).Return(nil, tc.lookupErr)
			log := &sgCLog{}

			w := do(newRouter(sgCExportHandler(t, versions, workflows, log)), req(http.MethodGet, sgCExportPath(), nil))

			require.Equal(t, tc.wantStatus, w.Code, w.Body.String())
			assert.Contains(t, w.Header().Get("Content-Type"), "application/problem+json")
			prob := decodeProblem(t, w)
			assert.Equal(t, tc.wantCode, prob["code"])
			assert.EqualValues(t, tc.wantStatus, prob["status"])
			if tc.wantDetail != "" {
				assert.Equal(t, tc.wantDetail, prob["detail"])
			}
			assert.Empty(t, w.Header().Get("Content-Disposition"), "no attachment on a failed export")
			assert.NotContains(t, w.Body.String(), "<definitions/>")
			assert.NotContains(t, w.Body.String(), driverText)

			if !tc.wantLogged {
				assert.Empty(t, log.errors)
				return
			}
			require.Len(t, log.errors, 1)
			fields := log.errors[0].fields
			assert.Equal(t, "request failed", log.errors[0].msg)
			assert.Equal(t, "INTERNAL_ERROR", fields["code"])
			assert.Equal(t, testTenantID.String(), fields["tenant_id"])
			assert.Equal(t, testUserID.String(), fields["user_id"])
			assert.Equal(t, sgCExportPath(), fields["path"])
			assert.Contains(t, fields["error"], driverText)
		})
	}
}

func TestExportBPMN_AnyVersionStatus_ReturnsThatVersionsXMLAndVersionedFilename(t *testing.T) {
	t.Parallel()

	four, one := int32(4), int32(1)
	cases := []struct {
		name         string
		status       domain.VersionStatus
		number       *int32
		wantFilename string
	}{
		{name: "archived version", status: domain.VersionStatusArchived, number: &four, wantFilename: "tender-review-v4.bpmn"},
		{name: "published version", status: domain.VersionStatusPublished, number: &one, wantFilename: "tender-review-v1.bpmn"},
		{name: "draft has no number", status: domain.VersionStatusDraft, wantFilename: "tender-review-vdraft.bpmn"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			versions := mocks.NewMockWorkflowVersionRepository(ctrl)
			workflows := mocks.NewMockWorkflowRepository(ctrl)
			xml := fmt.Sprintf("<definitions id=%q/>", tc.name)
			versions.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).Return(&domain.WorkflowVersion{
				ID: testVerID, WorkflowID: testWFID, TenantID: testTenantID,
				Status: tc.status, VersionNumber: tc.number, BPMNXML: xml,
			}, nil)
			workflows.EXPECT().GetByID(gomock.Any(), testTenantID, testWFID).Return(
				&domain.Workflow{ID: testWFID, TenantID: testTenantID, TemplateKey: "tender-review"}, nil)

			w := do(newRouter(sgCExportHandler(t, versions, workflows, &sgCLog{})), req(http.MethodGet, sgCExportPath(), nil))

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			assert.Equal(t, xml, w.Body.String())
			assert.Equal(t, "application/xml; charset=utf-8", w.Header().Get("Content-Type"))
			assert.Equal(t, "attachment; filename="+tc.wantFilename, w.Header().Get("Content-Disposition"))
		})
	}
}
