package validator

import (
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-connectors/pkg/registry"

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
