package service_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestDraftService_Update_OmittedName_KeepsTheStoredName(t *testing.T) {
	t.Parallel()

	strPtr := func(s string) *string { return &s }
	cases := []struct {
		name     string
		req      service.UpdateDraftReq
		wantName string
		wantDesc string
	}{
		{
			name:     "a filled name is stored as sent",
			req:      service.UpdateDraftReq{Name: strPtr(" Renamed "), RecordVersion: 1},
			wantName: " Renamed ",
			wantDesc: "stored description",
		},
		{
			name:     "omitted name keeps the stored name",
			req:      service.UpdateDraftReq{Description: strPtr("new description"), RecordVersion: 1},
			wantName: "Stored Name",
			wantDesc: "new description",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			workflows := mocks.NewMockWorkflowRepository(ctrl)
			versions := mocks.NewMockWorkflowVersionRepository(ctrl)
			svc := service.NewDraftService(service.DraftDeps{
				Transactor: txPassthrough(ctrl),
				Workflows:  workflows,
				Versions:   versions,
				Assignees:  mocks.NewMockAssigneeRepository(ctrl),
				Compiler:   mocks.NewMockPlanCompiler(ctrl),
			})
			tenantID, userID, wfID, draftID := uuid.New(), uuid.New(), uuid.New(), uuid.New()

			versions.EXPECT().GetDraft(gomock.Any(), tenantID, wfID).
				Return(&domain.WorkflowVersion{ID: draftID, WorkflowID: wfID, TenantID: tenantID, RecordVersion: 1}, nil)
			workflows.EXPECT().GetByID(gomock.Any(), tenantID, wfID).
				Return(&domain.Workflow{ID: wfID, TenantID: tenantID, Name: "Stored Name", Description: "stored description"}, nil)
			workflows.EXPECT().UpdateMetadata(gomock.Any(), tenantID, wfID, tc.wantName, tc.wantDesc).Return(nil)
			versions.EXPECT().UpdateDraft(gomock.Any(), gomock.Cond(func(v *domain.WorkflowVersion) bool {
				return v.ID == draftID && v.WorkflowID == wfID && v.TenantID == tenantID
			})).Return(nil)

			got, err := svc.Update(t.Context(), tenantID, userID, wfID, tc.req)

			require.NoError(t, err)
			assert.Equal(t, draftID, got.ID)
		})
	}
}
