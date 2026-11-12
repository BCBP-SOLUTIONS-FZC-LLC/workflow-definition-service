package service_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestDraftService_Update_StoredValidationErrorsFollowTheBPMN(t *testing.T) {
	staleErrors := `[{"code":"MISSING_START_EVENT","node_id":"P1","message":"no start","severity":"error"}]`
	newXML := "<bpmn2/>"
	newName := "renamed"

	tests := []struct {
		name         string
		req          service.UpdateDraftReq
		wantIsValid  bool
		wantErrors   *string
		wantMetadata bool
	}{
		{
			name:        "new BPMN drops the errors that described the old BPMN",
			req:         service.UpdateDraftReq{BPMNXML: &newXML, RecordVersion: 3},
			wantIsValid: true,
			wantErrors:  nil,
		},
		{
			name:         "new BPMN with a rename still drops the errors",
			req:          service.UpdateDraftReq{BPMNXML: &newXML, Name: &newName, RecordVersion: 3},
			wantIsValid:  true,
			wantErrors:   nil,
			wantMetadata: true,
		},
		{
			name:         "a rename alone keeps the errors and the invalid flag",
			req:          service.UpdateDraftReq{Name: &newName, RecordVersion: 3},
			wantIsValid:  false,
			wantErrors:   &staleErrors,
			wantMetadata: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			cache := mocks.NewMockCacheStore(ctrl)
			wfRepo := mocks.NewMockWorkflowRepository(ctrl)
			vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
			svc := newDraftSvc(ctrl, cache, wfRepo, vRepo)

			tenantID, userID, wfID := uuid.New(), uuid.New(), uuid.New()
			stale := staleErrors
			plan := `{"plan":"old"}`
			draft := &domain.WorkflowVersion{
				ID: uuid.New(), RecordVersion: 3, BPMNXML: "<old/>",
				IsValid: false, ValidationErrorsJSON: &stale, CompiledPlanJSON: &plan, ArtifactHash: "old-hash",
			}
			cache.EXPECT().SetNX(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil)
			cache.EXPECT().DelIfEqual(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
			vRepo.EXPECT().GetDraft(gomock.Any(), tenantID, wfID).Return(draft, nil)
			if tc.wantMetadata {
				wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{Name: "old", Description: "d"}, nil)
				wfRepo.EXPECT().UpdateMetadata(gomock.Any(), tenantID, wfID, gomock.Any(), "d").Return(nil)
			}
			var saved *domain.WorkflowVersion
			vRepo.EXPECT().UpdateDraft(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, v *domain.WorkflowVersion) error {
					copied := *v
					saved = &copied
					return nil
				})

			if _, err := svc.Update(context.Background(), tenantID, userID, wfID, tc.req); err != nil {
				t.Fatalf("Update: %v", err)
			}

			if saved == nil {
				t.Fatal("the draft was not saved")
			}
			if saved.IsValid != tc.wantIsValid {
				t.Errorf("IsValid = %v, want %v", saved.IsValid, tc.wantIsValid)
			}
			switch {
			case tc.wantErrors == nil && saved.ValidationErrorsJSON != nil:
				t.Errorf("ValidationErrorsJSON = %q, want it cleared", *saved.ValidationErrorsJSON)
			case tc.wantErrors != nil && (saved.ValidationErrorsJSON == nil || *saved.ValidationErrorsJSON != *tc.wantErrors):
				t.Errorf("ValidationErrorsJSON = %v, want %q kept", saved.ValidationErrorsJSON, *tc.wantErrors)
			}
			if tc.req.BPMNXML != nil && (saved.CompiledPlanJSON != nil || saved.ArtifactHash != "") {
				t.Errorf("compiled plan and hash must be dropped with new BPMN, got plan=%v hash=%q", saved.CompiledPlanJSON, saved.ArtifactHash)
			}
		})
	}
}
