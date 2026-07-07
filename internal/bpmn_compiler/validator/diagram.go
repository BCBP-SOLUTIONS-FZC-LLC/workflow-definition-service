package validator

import (
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func ValidateDiagramCompleteness(proc *bpmncore.BPMNProcess, defs *bpmncore.BPMNDefinitions) []domain.BPMNValidationError {
	if len(defs.Diagrams) == 0 {
		return AppendErr(nil, domain.BPMNErrMissingDiagram, "",
			"BPMN document must include a <bpmndi:BPMNDiagram> element; without it the workflow cannot be displayed or edited in a modeler")
	}
	shapeSet := buildDiagramShapeSet(defs)
	edgeSet := buildDiagramEdgeSet(defs)

	var errs []domain.BPMNValidationError
	errs = append(errs, checkProcessShapes(proc, shapeSet, edgeSet)...)
	for i := range proc.SubProcesses {
		inner := bpmncore.SubProcToProcess(&proc.SubProcesses[i])
		errs = append(errs, checkProcessShapes(inner, shapeSet, edgeSet)...)
	}
	return errs
}

func buildDiagramShapeSet(defs *bpmncore.BPMNDefinitions) map[string]struct{} {
	m := make(map[string]struct{})
	for _, d := range defs.Diagrams {
		for _, s := range d.Plane.Shapes {
			m[s.BPMNElement] = struct{}{}
		}
	}
	return m
}

func buildDiagramEdgeSet(defs *bpmncore.BPMNDefinitions) map[string]struct{} {
	m := make(map[string]struct{})
	for _, d := range defs.Diagrams {
		for _, e := range d.Plane.Edges {
			m[e.BPMNElement] = struct{}{}
		}
	}
	return m
}

func checkProcessShapes(proc *bpmncore.BPMNProcess, shapeSet, edgeSet map[string]struct{}) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	requireShape := func(id string) {
		if _, ok := shapeSet[id]; !ok {
			errs = AppendErr(errs, domain.BPMNErrMissingDiagramShape, id,
				fmt.Sprintf("element %q has no BPMNShape in the diagram; add it in your modeler so the workflow can be displayed and edited", id))
		}
	}
	for _, e := range proc.StartEvents {
		requireShape(e.ID)
	}
	for _, e := range proc.EndEvents {
		requireShape(e.ID)
	}
	for _, t := range proc.UserTasks {
		requireShape(t.ID)
	}
	for _, t := range proc.SendTasks {
		requireShape(t.ID)
	}
	for _, t := range proc.ReceiveTasks {
		requireShape(t.ID)
	}
	for _, gw := range proc.ParallelGateways {
		requireShape(gw.ID)
	}
	for _, gw := range proc.ExclusiveGateways {
		requireShape(gw.ID)
	}
	for _, gw := range proc.InclusiveGateways {
		requireShape(gw.ID)
	}
	for _, sp := range proc.SubProcesses {
		requireShape(sp.ID)
	}
	for _, ca := range proc.CallActivities {
		requireShape(ca.ID)
	}
	for _, be := range proc.BoundaryEvents {
		requireShape(be.ID)
	}
	for _, sf := range proc.SequenceFlows {
		if _, ok := edgeSet[sf.ID]; !ok {
			errs = AppendErr(errs, domain.BPMNErrMissingDiagramShape, sf.ID,
				fmt.Sprintf("sequence flow %q has no BPMNEdge in the diagram; add it in your modeler so the workflow can be displayed and edited", sf.ID))
		}
	}
	return errs
}
