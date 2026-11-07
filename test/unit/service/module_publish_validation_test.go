package service_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

const modulePublishCycleXML = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="Def_Cycle" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="%s" name="Cycle" isExecutable="true">
    <bpmn:startEvent id="Cycle_Start"/>
    <bpmn:callActivity id="%s">
      <bpmn:extensionElements>
        <zeebe:calledElement processId="%s" propagateAllChildVariables="false"/>
      </bpmn:extensionElements>
    </bpmn:callActivity>
    <bpmn:endEvent id="Cycle_End"/>
    <bpmn:sequenceFlow id="Cycle_F1" sourceRef="Cycle_Start" targetRef="%[2]s"/>
    <bpmn:sequenceFlow id="Cycle_F2" sourceRef="%[2]s" targetRef="Cycle_End"/>
  </bpmn:process>
</bpmn:definitions>`

func TestModuleService_Publish_ModuleThatCallsItselfAtLatest_IsRefusedAsACycle(t *testing.T) {
	cases := []struct {
		name     string
		draftXML string
		library  map[libraryKey]*domain.ModuleVersion
		wantNode string
	}{
		{
			name:     "the draft calls its own process id",
			draftXML: selfCallingModuleBPMN,
			wantNode: "PR_Again",
		},
		{
			name:     "a library module the draft calls calls the draft back",
			draftXML: fmt.Sprintf(modulePublishCycleXML, "Process_Alpha", "Alpha_Call", "Process_Beta"),
			library: map[libraryKey]*domain.ModuleVersion{
				{"Process_Beta", 0}: libraryVersion(uuid.New(), "Process_Beta", 1,
					fmt.Sprintf(modulePublishCycleXML, "Process_Beta", "Beta_Call", "Process_Alpha")),
			},
			wantNode: "Alpha_Call",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			tx := mocks.NewMockTransactor(ctrl)
			versions := mocks.NewMockModuleVersionRepository(ctrl)
			svc := service.NewModuleService(service.ModuleDeps{
				Transactor: tx, Versions: versions, Compiler: bpmn_compiler.New(),
			})
			tenantID, moduleID, versionID := uuid.New(), uuid.New(), uuid.New()
			processID := "Process_PrepReview"
			if tc.library != nil {
				processID = "Process_Alpha"
			}
			versions.EXPECT().GetByID(gomock.Any(), tenantID, versionID).Return(&domain.ModuleVersion{
				ID: versionID, ModuleID: moduleID, Status: domain.VersionStatusDraft, Scope: domain.ScopeTenant,
				ProcessID: processID, BPMNXML: tc.draftXML,
			}, nil)
			expectLibrary(versions, tenantID, tc.library)

			got, err := svc.Publish(context.Background(), tenantID, uuid.New(), moduleID, versionID)

			var refused *domain.ValidationFailedError
			require.ErrorAs(t, err, &refused)
			require.Len(t, refused.Errors, 1, "%+v", refused.Errors)
			assert.Equal(t, domain.BPMNErrCalledElementCycle, refused.Errors[0].Code)
			assert.Equal(t, tc.wantNode, refused.Errors[0].NodeID)
			assert.Contains(t, refused.Errors[0].Message, processID)
			assert.Nil(t, got)
		})
	}
}

func TestModuleService_Publish_ValidationFindings_DecideWhetherThePublishProceeds(t *testing.T) {
	blocking := domain.BPMNValidationError{
		Code: "MISSING_END_EVENT", NodeID: "Node_1", Message: "process has no end event", Severity: domain.SeverityError,
	}
	otherBlocking := domain.BPMNValidationError{
		Code: "DANGLING_FLOW", NodeID: "Flow_2", Message: "flow has no target", Severity: domain.SeverityError,
	}
	warning := domain.BPMNValidationError{
		Code: "UNUSED_LANE", NodeID: "Lane_1", Message: "lane has no tasks", Severity: domain.SeverityWarning,
	}
	cases := []struct {
		name         string
		findings     []domain.BPMNValidationError
		wantRefusals []domain.BPMNValidationError
	}{
		{
			name:         "one error finding refuses the publish",
			findings:     []domain.BPMNValidationError{blocking},
			wantRefusals: []domain.BPMNValidationError{blocking},
		},
		{
			name:         "every error finding is reported and warnings are left out",
			findings:     []domain.BPMNValidationError{warning, blocking, otherBlocking},
			wantRefusals: []domain.BPMNValidationError{blocking, otherBlocking},
		},
		{
			name:     "warnings alone let the publish proceed",
			findings: []domain.BPMNValidationError{warning, warning},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			svc, m := newModuleService(ctrl)
			tenantID, moduleID, versionID := uuid.New(), uuid.New(), uuid.New()
			m.versions.EXPECT().GetByID(gomock.Any(), tenantID, versionID).Return(&domain.ModuleVersion{
				ID: versionID, ModuleID: moduleID, Status: domain.VersionStatusDraft, Scope: domain.ScopeTenant,
				ProcessID: "Process_X",
			}, nil)
			expectModuleValidation(m.compiler, "Process_X", tc.findings, nil)
			if tc.wantRefusals == nil {
				passthrough(m.tx)
				m.versions.EXPECT().NextVersionNumber(gomock.Any(), moduleID).Return(int32(4), nil)
				m.versions.EXPECT().Publish(gomock.Any(), tenantID, versionID, int32(4)).Return(nil)
				m.modules.EXPECT().UpdateActiveVersion(gomock.Any(), moduleID, &versionID).Return(nil)
			}

			got, err := svc.Publish(context.Background(), tenantID, uuid.New(), moduleID, versionID)

			if tc.wantRefusals != nil {
				var refused *domain.ValidationFailedError
				require.ErrorAs(t, err, &refused)
				assert.Equal(t, tc.wantRefusals, refused.Errors)
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, got.VersionNumber)
			assert.Equal(t, int32(4), *got.VersionNumber)
			assert.Equal(t, domain.VersionStatusPublished, got.Status)
		})
	}
}

func TestModuleService_Publish_StoreUnavailable_KeepsTheUnavailableCause(t *testing.T) {
	unavailable := fmt.Errorf("%w: connection refused", domain.ErrUpstreamUnavailable)
	cases := []struct {
		name  string
		setup func(m moduleMocks, tenantID, moduleID, versionID uuid.UUID)
	}{
		{
			name: "version lookup",
			setup: func(m moduleMocks, tenantID, _, versionID uuid.UUID) {
				m.versions.EXPECT().GetByID(gomock.Any(), tenantID, versionID).Return(nil, unavailable)
			},
		},
		{
			name: "publish transaction",
			setup: func(m moduleMocks, tenantID, moduleID, versionID uuid.UUID) {
				m.versions.EXPECT().GetByID(gomock.Any(), tenantID, versionID).Return(&domain.ModuleVersion{
					ID: versionID, ModuleID: moduleID, Status: domain.VersionStatusDraft, Scope: domain.ScopeTenant,
				}, nil)
				expectModuleValidation(m.compiler, "", nil, nil)
				m.tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).Return(unavailable)
			},
		},
		{
			name: "version number inside the transaction",
			setup: func(m moduleMocks, tenantID, moduleID, versionID uuid.UUID) {
				m.versions.EXPECT().GetByID(gomock.Any(), tenantID, versionID).Return(&domain.ModuleVersion{
					ID: versionID, ModuleID: moduleID, Status: domain.VersionStatusDraft, Scope: domain.ScopeTenant,
				}, nil)
				expectModuleValidation(m.compiler, "", nil, nil)
				passthrough(m.tx)
				m.versions.EXPECT().NextVersionNumber(gomock.Any(), moduleID).Return(int32(0), unavailable)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			svc, m := newModuleService(ctrl)
			tenantID, moduleID, versionID := uuid.New(), uuid.New(), uuid.New()
			tc.setup(m, tenantID, moduleID, versionID)

			got, err := svc.Publish(context.Background(), tenantID, uuid.New(), moduleID, versionID)

			assert.True(t, errors.Is(err, domain.ErrUpstreamUnavailable), "got %v", err)
			assert.Nil(t, got)
		})
	}
}
