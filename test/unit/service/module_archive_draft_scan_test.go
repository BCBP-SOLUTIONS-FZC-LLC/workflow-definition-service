package service_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestModuleService_Archive_DraftScan_ParsesOnlyDraftsThatMentionTheModuleProcessID(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)
	passthrough(m.tx)

	tenantID, userID, moduleID, activeID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	const processID = "mod_approval"
	m.modules.EXPECT().GetByID(gomock.Any(), tenantID, moduleID).
		Return(&domain.Module{ID: moduleID, ProcessID: processID, Scope: domain.ScopeTenant, ActiveVersionID: &activeID}, nil).Times(2)
	m.workflowVersions.EXPECT().ListModuleUsages(gomock.Any(), tenantID, moduleID).Return(nil, nil)

	const unrelatedDrafts = 40
	drafts := make([]*domain.DraftSource, 0, unrelatedDrafts+2)
	for i := range unrelatedDrafts {
		drafts = append(drafts, &domain.DraftSource{
			WorkflowID: uuid.New(), WorkflowKey: fmt.Sprintf("wf-%d", i),
			BPMNXML: `<definitions><process id="p` + fmt.Sprint(i) + `"/></definitions>`,
		})
	}
	caller := &domain.DraftSource{
		WorkflowID: uuid.New(), WorkflowKey: "caller",
		BPMNXML: `<definitions><callActivity><calledElement processId="` + processID + `"/></callActivity></definitions>`,
	}
	lookalike := &domain.DraftSource{
		WorkflowID: uuid.New(), WorkflowKey: "lookalike",
		BPMNXML: `<definitions><callActivity><calledElement processId="` + processID + `_other"/></callActivity></definitions>`,
	}
	drafts = append(drafts, caller, lookalike)
	m.workflowVersions.EXPECT().ListDraftSources(gomock.Any(), tenantID).Return(drafts, nil)

	var parsed []string
	m.compiler.EXPECT().ModuleCalls(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, xml string) ([]domain.ModuleCall, []domain.BPMNValidationError, error) {
			parsed = append(parsed, xml)
			if xml == caller.BPMNXML {
				return []domain.ModuleCall{{CallID: "c", ProcessID: processID}}, nil, nil
			}
			return []domain.ModuleCall{{CallID: "c", ProcessID: processID + "_other"}}, nil, nil
		}).AnyTimes()

	err := svc.Archive(context.Background(), tenantID, userID, moduleID)

	var inUse *domain.ModuleInUseError
	require.ErrorAs(t, err, &inUse)
	require.Len(t, inUse.Uses, 1)
	assert.Equal(t, "caller", inUse.Uses[0].WorkflowKey)
	assert.ElementsMatch(t, []string{caller.BPMNXML, lookalike.BPMNXML}, parsed,
		"the %d drafts that never mention the process id are not parsed", unrelatedDrafts)
	for _, xml := range parsed {
		assert.True(t, strings.Contains(xml, processID))
	}
}

func TestModuleService_Archive_DraftScan_ACharacterReferenceCannotHideACall(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)
	passthrough(m.tx)

	tenantID, moduleID, activeID := uuid.New(), uuid.New(), uuid.New()
	m.modules.EXPECT().GetByID(gomock.Any(), tenantID, moduleID).
		Return(&domain.Module{ID: moduleID, ProcessID: "mod_approval", Scope: domain.ScopeTenant, ActiveVersionID: &activeID}, nil).Times(2)
	m.workflowVersions.EXPECT().ListModuleUsages(gomock.Any(), tenantID, moduleID).Return(nil, nil)
	hidden := `<calledElement processId="mod_&#97;pproval"/>`
	m.workflowVersions.EXPECT().ListDraftSources(gomock.Any(), tenantID).Return(
		[]*domain.DraftSource{{WorkflowID: uuid.New(), WorkflowKey: "hidden", BPMNXML: hidden}}, nil)
	m.compiler.EXPECT().ModuleCalls(gomock.Any(), hidden).
		Return([]domain.ModuleCall{{CallID: "c", ProcessID: "mod_approval"}}, nil, nil)

	err := svc.Archive(context.Background(), tenantID, uuid.New(), moduleID)

	assert.ErrorIs(t, err, domain.ErrModuleInUse)
}
