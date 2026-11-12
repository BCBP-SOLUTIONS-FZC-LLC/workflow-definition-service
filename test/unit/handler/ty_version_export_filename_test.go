package handler_test

import (
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

func TestExportBPMN_TemplateKeyWithSpaceOrNonASCII_SendsTheEncodedAttachmentName(t *testing.T) {
	t.Parallel()

	one := int32(1)
	cases := []struct {
		name            string
		templateKey     string
		wantDisposition string
	}{
		{
			name:            "space is quoted",
			templateKey:     "my key",
			wantDisposition: `attachment; filename="my key-v1.bpmn"`,
		},
		{
			name:            "non-ASCII letter is RFC 2231 encoded",
			templateKey:     "clé",
			wantDisposition: "attachment; filename*=utf-8''cl%C3%A9-v1.bpmn",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			versions := mocks.NewMockWorkflowVersionRepository(ctrl)
			workflows := mocks.NewMockWorkflowRepository(ctrl)
			const xmlBody = `<definitions id="é &amp; key"/>`
			versions.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).Return(&domain.WorkflowVersion{
				ID: testVerID, WorkflowID: testWFID, TenantID: testTenantID,
				Status: domain.VersionStatusPublished, VersionNumber: &one, BPMNXML: xmlBody,
			}, nil)
			workflows.EXPECT().GetByID(gomock.Any(), testTenantID, testWFID).Return(
				&domain.Workflow{ID: testWFID, TenantID: testTenantID, TemplateKey: tc.templateKey}, nil)
			svc := service.NewVersionService(service.VersionDeps{Versions: versions, Workflows: workflows})
			h := handler.New(handler.Services{Versions: &fakeVersionSvc{export: svc.Export}})
			path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/export"

			w := do(newRouter(h), req(http.MethodGet, path, nil))

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			assert.Equal(t, tc.wantDisposition, w.Header().Get("Content-Disposition"))
			assert.Equal(t, "application/xml; charset=utf-8", w.Header().Get("Content-Type"))
			assert.Equal(t, xmlBody, w.Body.String())
		})
	}
}
