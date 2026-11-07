package service_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/enums"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type sgAMembershipCallCounter struct{ calls atomic.Int32 }

func (c *sgAMembershipCallCounter) CheckEligibility(context.Context, uuid.UUID, uuid.UUID, string, string) (domain.Eligibility, error) {
	c.calls.Add(1)
	return domain.Eligibility{Eligible: true}, nil
}

func TestPublish_PlanWithNoHumanDefaultAssignees_AsksMembershipNothing(t *testing.T) {
	t.Parallel()
	someone := uuid.Must(uuid.NewV7()).String()
	cases := []struct {
		name string
		plan *dsl.CompiledPlan
	}{
		{"human tasks name no default user", &dsl.CompiledPlan{Name: "p", Departments: []dsl.DepartmentDef{
			{ID: "a", IAMDepartmentID: uuid.NewString(), Stages: []dsl.StageDef{{NodeID: "t1", Type: "review", Role: "reviewer"}}},
		}}},
		{"a plan with no stages", &dsl.CompiledPlan{Name: "p", Departments: []dsl.DepartmentDef{{ID: "a", IAMDepartmentID: uuid.NewString()}}}},
		{"only a send task carries an assignee, and it creates no human task", &dsl.CompiledPlan{Name: "p", Departments: []dsl.DepartmentDef{
			{ID: "a", IAMDepartmentID: uuid.NewString(), Stages: []dsl.StageDef{
				{NodeID: "s1", Type: string(enums.StageTypeSendTask), Role: "reviewer", DefaultAssignees: []string{someone}},
				{NodeID: "c1", Type: "review", ConnectorType: "storage", Role: "reviewer", DefaultAssignees: []string{someone}},
			}},
		}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			tx := txPassthrough(ctrl)
			wfRepo := mocks.NewMockWorkflowRepository(ctrl)
			vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
			assignees := mocks.NewMockAssigneeRepository(ctrl)
			compiler := mocks.NewMockPlanCompiler(ctrl)
			membership := &sgAMembershipCallCounter{}
			svc := service.NewVersionService(service.VersionDeps{
				Transactor: tx, Workflows: wfRepo, Versions: vRepo, Assignees: assignees, Compiler: compiler, Membership: membership,
			})
			tenantID, userID, wfID, vID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
			draft := &domain.WorkflowVersion{ID: vID, WorkflowID: wfID, TenantID: tenantID, Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>", RecordVersion: 4}
			vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(draft, nil)
			compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
			compiler.EXPECT().BundleModules("<bpmn/>", gomock.Len(0)).Return("<bpmn/>", nil)
			compiler.EXPECT().CompileForPublish(gomock.Any(), "<bpmn/>").Return(collabOf(tc.plan), nil)
			compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("h1", nil)
			wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{}, nil)
			vRepo.EXPECT().NextVersionNumber(gomock.Any(), tenantID, wfID).Return(int32(1), nil)
			vRepo.EXPECT().Publish(gomock.Any(), tenantID, vID, int64(4), int32(1), gomock.Any(), "h1", gomock.Any()).Return(nil)
			assignees.EXPECT().DeleteByVersion(gomock.Any(), tenantID, vID).Return(nil)
			wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, &vID).Return(nil)

			got, err := svc.Publish(context.Background(), tenantID, userID, wfID, vID, false)

			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, domain.VersionStatusPublished, got.Status)
			assert.Zero(t, membership.calls.Load(), "no assignee to check, so membership is not asked")
		})
	}
}
