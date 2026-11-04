package validator

import (
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func ValidateConditionExprs(proc *bpmncore.BPMNProcess, g *bpmncore.Graph, backEdges map[[2]string]bool) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	errs = append(errs, validateDefaultFlow(proc, g, backEdges)...)
	errs = append(errs, validateInclusiveGatewayConditions(proc, g, backEdges)...)
	exclusive := make(map[string]bool, len(proc.ExclusiveGateways))
	for _, gw := range proc.ExclusiveGateways {
		exclusive[gw.ID] = true
	}
	for _, sf := range proc.SequenceFlows {
		if len(g.Outgoing[sf.SourceRef]) < 2 && !exclusive[sf.SourceRef] {
			continue
		}
		cond := sf.ConditionExpression
		if cond == "" {
			continue
		}
		if len(cond) > maxCondExpr {
			errs = AppendErr(errs, domain.BPMNErrInvalidConditionExpression, sf.ID,
				fmt.Sprintf("conditionExpression exceeds %d character limit", maxCondExpr))
			continue
		}
		if _, err := dsl.ParseCondition(cond); err != nil {
			errs = AppendErr(errs, domain.BPMNErrInvalidConditionExpression, sf.ID, err.Error())
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

func validateInclusiveGatewayConditions(proc *bpmncore.BPMNProcess, g *bpmncore.Graph, backEdges map[[2]string]bool) []domain.BPMNValidationError {
	conditional := make(map[[2]string]bool)
	for _, sf := range proc.SequenceFlows {
		if sf.ConditionExpression != "" {
			conditional[[2]string{sf.SourceRef, sf.TargetRef}] = true
		}
	}
	var errs []domain.BPMNValidationError
	for _, gw := range proc.InclusiveGateways {
		if forwardOutCount(gw.ID, g, backEdges) < 2 {
			continue
		}
		for _, next := range g.Outgoing[gw.ID] {
			if conditional[[2]string{gw.ID, next}] {
				errs = AppendErr(errs, domain.BPMNErrUnsupportedElement, gw.ID,
					"an inclusive gateway with a conditionExpression on an outgoing flow is not supported: it would run only some of its branches; remove the conditions to run every branch and wait for all of them, or use an exclusive gateway to choose one")
				break
			}
		}
	}
	return errs
}
