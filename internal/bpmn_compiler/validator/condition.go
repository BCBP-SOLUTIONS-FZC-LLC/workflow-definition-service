package validator

import (
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func ValidateConditionExprs(proc *bpmncore.BPMNProcess, g *bpmncore.Graph) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	for _, sf := range proc.SequenceFlows {
		if len(g.Outgoing[sf.SourceRef]) < 2 {
			continue
		}
		cond := sf.ConditionExpression
		if cond == "" {
			continue
		}
		if len(cond) > maxCondExpr {
			errs = AppendErr(errs, domain.BPMNErrInvalidConditionExpression, sf.ID,
				fmt.Sprintf("conditionExpression exceeds %d character limit", maxCondExpr))
		}
	}
	return errs
}
