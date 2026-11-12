package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	bpmncompiler "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestDraftService_Update_WorkflowMetadataStepFails_ErrorIsWrappedAndTheDraftRowIsNotWritten(t *testing.T) {
	t.Parallel()

	strPtr := func(s string) *string { return &s }
	cases := []struct {
		name        string
		req         service.UpdateDraftReq
		readErr     error
		updateErr   error
		wantPrefix  string
		wantName    string
		wantDesc    string
		wantUpdated bool
	}{
		{
			name:       "the workflow read fails and keeps its not-found type",
			req:        service.UpdateDraftReq{Description: strPtr("new description"), RecordVersion: 1},
			readErr:    domain.ErrNotFound,
			wantPrefix: "update draft: get workflow: ",
		},
		{
			name:        "the metadata write fails for a name-only change and the description is carried over",
			req:         service.UpdateDraftReq{Name: strPtr("new name"), RecordVersion: 1},
			updateErr:   errors.New("metadata write refused"),
			wantPrefix:  "update draft: update workflow metadata: ",
			wantName:    "new name",
			wantDesc:    "old description",
			wantUpdated: true,
		},
		{
			name:        "the metadata write fails for a description-only change and the name is carried over",
			req:         service.UpdateDraftReq{Description: strPtr("new description"), RecordVersion: 1},
			updateErr:   errors.New("metadata write refused"),
			wantPrefix:  "update draft: update workflow metadata: ",
			wantName:    "old name",
			wantDesc:    "new description",
			wantUpdated: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			tx := &w5bTx{}
			workflows := mocks.NewMockWorkflowRepository(ctrl)
			versions := mocks.NewMockWorkflowVersionRepository(ctrl)
			svc := service.NewDraftService(service.DraftDeps{
				Transactor: tx,
				Workflows:  workflows,
				Versions:   versions,
				Assignees:  mocks.NewMockAssigneeRepository(ctrl),
				Compiler:   bpmncompiler.New(),
			})
			tenantID, userID, wfID := uuid.New(), uuid.New(), uuid.New()

			versions.EXPECT().GetDraft(gomock.Any(), tenantID, wfID).
				Return(&domain.WorkflowVersion{ID: uuid.New(), RecordVersion: 1}, nil)
			if tc.readErr != nil {
				workflows.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(nil, tc.readErr)
			} else {
				workflows.EXPECT().GetByID(gomock.Any(), tenantID, wfID).
					Return(&domain.Workflow{ID: wfID, Name: "old name", Description: "old description"}, nil)
			}
			var gotName, gotDesc string
			var updated bool
			if tc.wantUpdated {
				workflows.EXPECT().UpdateMetadata(gomock.Any(), tenantID, wfID, gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, _, _ uuid.UUID, name, description string) error {
						gotName, gotDesc, updated = name, description, true
						return tc.updateErr
					})
			}

			got, err := svc.Update(context.Background(), tenantID, userID, wfID, tc.req)

			require.Error(t, err)
			assert.Nil(t, got)
			assert.True(t, strings.HasPrefix(err.Error(), tc.wantPrefix), "err = %q", err)
			if tc.readErr != nil {
				assert.ErrorIs(t, err, tc.readErr)
			}
			if tc.updateErr != nil {
				assert.ErrorIs(t, err, tc.updateErr)
			}
			assert.Equal(t, tc.wantUpdated, updated)
			assert.Equal(t, tc.wantName, gotName)
			assert.Equal(t, tc.wantDesc, gotDesc)
			assert.Equal(t, 1, tx.started, "the metadata change runs inside one transaction")
			assert.Equal(t, 1, tx.rollbacks, "the failure leaves the transaction through its error so it rolls back")
			assert.Zero(t, tx.commits)
			assert.Empty(t, tx.persisted())
		})
	}
}
