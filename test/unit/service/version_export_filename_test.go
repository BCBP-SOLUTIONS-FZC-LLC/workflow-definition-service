package service_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestVersionService_Export_FileNameNeverContainsPathSeparatorsOrControlCharacters(t *testing.T) {
	cases := []struct {
		name string
		key  string
		want string
	}{
		{"an ordinary key is unchanged", "tender-review_2", "tender-review_2-v3.bpmn"},
		{"a non-ASCII letter is kept", "überprüfung", "überprüfung-v3.bpmn"},
		{"slashes become underscores", "a/../b", "a_.._b-v3.bpmn"},
		{"backslashes become underscores", `a\..\b`, "a_.._b-v3.bpmn"},
		{"a leading slash is replaced", "/etc/passwd", "_etc_passwd-v3.bpmn"},
		{"a line break is replaced", "a\r\nb", "a__b-v3.bpmn"},
		{"a NUL and a tab are replaced", "a\x00b\tc", "a_b_c-v3.bpmn"},
		{"DEL is replaced", "a\x7fb", "a_b-v3.bpmn"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			wfRepo := mocks.NewMockWorkflowRepository(ctrl)
			vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
			svc := service.NewVersionService(service.VersionDeps{Workflows: wfRepo, Versions: vRepo})
			tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
			number := int32(3)
			vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(&domain.WorkflowVersion{ID: vID, WorkflowID: wfID, BPMNXML: "<bpmn/>", VersionNumber: &number}, nil)
			wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{TemplateKey: tc.key}, nil)

			xml, filename, err := svc.Export(context.Background(), tenantID, wfID, vID)

			require.NoError(t, err)
			assert.Equal(t, "<bpmn/>", xml)
			assert.Equal(t, tc.want, filename)
		})
	}
}
