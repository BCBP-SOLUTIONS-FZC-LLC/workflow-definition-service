package validator

import (
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func ValidateConditionExprs(proc *bpmncore.BPMNProcess, g *bpmncore.Graph, backEdges map[[2]string]bool) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	errs = append(errs, validateDefaultFlow(proc, g, backEdges)...)
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

func validateDefaultFlow(proc *bpmncore.BPMNProcess, g *bpmncore.Graph, backEdges map[[2]string]bool) []domain.BPMNValidationError {
	conditional := make(map[[2]string]bool)
	for _, sf := range proc.SequenceFlows {
		if sf.ConditionExpression != "" {
			conditional[[2]string{sf.SourceRef, sf.TargetRef}] = true
		}
	}
	var errs []domain.BPMNValidationError
	for _, gw := range proc.ExclusiveGateways {
		forward, unconditioned := 0, 0
		for _, next := range g.Outgoing[gw.ID] {
			edge := [2]string{gw.ID, next}
			if backEdges[edge] {
				continue
			}
			forward++
			if !conditional[edge] {
				unconditioned++
			}
		}
		if forward > 1 && unconditioned > 1 {
			errs = AppendWarn(errs, domain.BPMNWarnGatewayBranchWithoutCondition, gw.ID,
				fmt.Sprintf("exclusive gateway has %d outgoing flows without a conditionExpression; only the first of them is ever taken, so give every flow but one default flow a condition", unconditioned))
		}
	}
	return errs
}
