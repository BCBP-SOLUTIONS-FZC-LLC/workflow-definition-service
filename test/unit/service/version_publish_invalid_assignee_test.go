package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"
	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestVersionService_Publish_InvalidAssigneeID_IsRefusedWithOrWithoutAMembershipClient(t *testing.T) {
	validV7, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("NewV7: %v", err)
	}
	tests := []struct {
		name           string
		defaultUsers   []string
		withMembership bool
	}{
		{name: "no membership client, not a UUID", defaultUsers: []string{"not-a-uuid"}},
		{name: "no membership client, valid user then a bad one", defaultUsers: []string{validV7.String(), "also-bad"}},
		{name: "no membership client, version 4 UUID", defaultUsers: []string{uuid.MustParse("6ba7b810-9dad-41d1-80b4-00c04fd430c8").String()}},
		{name: "membership client, not a UUID", defaultUsers: []string{"not-a-uuid"}, withMembership: true},
		{name: "membership client, version 4 UUID", defaultUsers: []string{uuid.MustParse("6ba7b810-9dad-41d1-80b4-00c04fd430c8").String()}, withMembership: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			tx := mocks.NewMockTransactor(ctrl)
			wfRepo := mocks.NewMockWorkflowRepository(ctrl)
			vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
			assignees := mocks.NewMockAssigneeRepository(ctrl)
			compiler := mocks.NewMockPlanCompiler(ctrl)
			var membership port.MembershipService
			if tc.withMembership {
				membership = mocks.NewMockMembershipService(ctrl)
			}
			svc := service.NewVersionService(service.VersionDeps{
				Transactor: tx, Workflows: wfRepo, Versions: vRepo,
				Assignees: assignees, Compiler: compiler, Membership: membership,
			})

			tenantID, userID, wfID, vID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
			plan := &dsl.CompiledPlan{
				Departments: []dsl.DepartmentDef{{
					ID:              "ops",
					IAMDepartmentID: uuid.New().String(),
					Stages: []dsl.StageDef{{
						Type:             "review",
						Role:             "reviewer",
						DefaultAssignees: tc.defaultUsers,
					}},
				}},
			}
			draft := &domain.WorkflowVersion{
				ID: vID, WorkflowID: wfID, TenantID: tenantID,
				Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>",
			}
			vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(draft, nil)
			compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
			compiler.EXPECT().BundleModules("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
			compiler.EXPECT().CompileForPublish(gomock.Any(), "<bpmn/>").Return(collabOf(plan), nil)
			compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("h", nil)
			wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{}, nil)

			got, err := svc.Publish(context.Background(), tenantID, userID, wfID, vID, true)

			if got != nil || !errors.Is(err, domain.ErrAssigneeIneligible) {
				t.Fatalf("Publish = %v, %v; want nil and ErrAssigneeIneligible with nothing written", got, err)
			}
		})
	}
}
