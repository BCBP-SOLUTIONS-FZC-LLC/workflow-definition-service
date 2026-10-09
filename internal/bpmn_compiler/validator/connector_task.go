package validator

import (
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-connectors/pkg/registry"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func ValidateConnectorTaskDef(taskID, connectorType string) []domain.BPMNValidationError {
	if _, ok := registry.All()[connectorType]; !ok {
		return AppendWarn(nil, domain.BPMNWarnUnknownConnectorType, taskID,
			fmt.Sprintf("connector type %q is not a defined connector in the workflow engine", connectorType))
	}
	return nil
}

func ValidateConnectorTaskNoAssignment(taskID string, ext bpmncore.BPMNExtensionElements) []domain.BPMNValidationError {
	if ext.AssignmentDefinition == nil {
		return nil
	}
	return AppendWarn(nil, domain.BPMNWarnConnectorTaskHasAssignment, taskID,
		"connector task also carries a <zeebe:assignmentDefinition>; connector tasks are automation-only and this assignment is ignored")
}

func ValidateConnectorNoAuthoredSecret(taskID, connectorType string, io *bpmncore.ZeebeIOMapping) []domain.BPMNValidationError {
	def, ok := registry.All()[connectorType]
	if !ok || io == nil {
		return nil
	}
	secrets := map[string]bool{}
	for _, f := range def.Inputs {
		if f.IsSecretRef() {
			secrets[f.Name] = true
		}
	}
	var errs []domain.BPMNValidationError
	for _, in := range io.Inputs {
		if secrets[in.Target] {
			errs = AppendErr(errs, domain.BPMNErrConnectorSecretAuthored, taskID,
				fmt.Sprintf("input %q of a %s connector is a credential; the tenant's stored credential is always used, so the diagram must not name one", in.Target, connectorType))
		}
	}
	return errs
}
