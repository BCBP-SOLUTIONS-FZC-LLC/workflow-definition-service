package validator

import (
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-connectors/pkg/registry"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

// ValidateConnectorTaskDef warns (never rejects) on an unrecognized connector
// type — mirrors ValidateTaskDef's unknown-stage-type leniency (design/LLD/workflow_connectors.md §4.2).
func ValidateConnectorTaskDef(taskID, connectorType string) []domain.BPMNValidationError {
	if _, ok := registry.All()[connectorType]; !ok {
		return AppendWarn(nil, domain.BPMNWarnUnknownConnectorType, taskID,
			fmt.Sprintf("connector type %q is not a defined connector in the workflow engine", connectorType))
	}
	return nil
}

// ValidateConnectorTaskNoAssignment warns when a connector-typed serviceTask
// also carries a <zeebe:assignmentDefinition> — connector tasks are fully
// automation-only (§5.3), so a co-present assignment is contradictory
// authoring, not a second, human-in-the-loop routing layer on top of it.
// BuildStageDef silently omits assignee/role for every connector stage
// regardless, so surfacing this as a warning (rather than staying silent)
// is the only way an author ever learns their assignment is being dropped.
func ValidateConnectorTaskNoAssignment(taskID string, ext bpmncore.BPMNExtensionElements) []domain.BPMNValidationError {
	if ext.AssignmentDefinition == nil {
		return nil
	}
	return AppendWarn(nil, domain.BPMNWarnConnectorTaskHasAssignment, taskID,
		"connector task also carries a <zeebe:assignmentDefinition>; connector tasks are automation-only and this assignment is ignored")
}
