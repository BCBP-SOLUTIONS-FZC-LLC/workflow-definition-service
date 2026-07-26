package element

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/pkg/dsl"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/validator"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

type CallActivityHandler struct{}

func (CallActivityHandler) NodeType() bpmncore.FlowNodeType { return bpmncore.NodeTypeCallActivity }

func (CallActivityHandler) Validate(nodeID string, proc *bpmncore.BPMNProcess, _ *bpmncore.Graph, defs *bpmncore.BPMNDefinitions, _ map[string]bpmncore.StageTypeHandler, _ map[bpmncore.FlowNodeType]bpmncore.ElementHandler) []domain.BPMNValidationError {
	ca := bpmncore.FindCallActivity(proc, nodeID)
	if ca == nil {
		return nil
	}
	return validator.ValidateCallActivities(&bpmncore.BPMNProcess{CallActivities: []bpmncore.BPMNCallActivity{*ca}}, defs)
}

func (CallActivityHandler) Compile(nodeID string, cs *bpmncore.CompileState) error {
	ca := bpmncore.FindCallActivity(cs.Proc, nodeID)
	if ca == nil {
		return fmt.Errorf("callActivity %q not found in process", nodeID)
	}

	// Timer and error boundary events on callActivities are not yet supported.
	// Message boundary events are allowed — they are collected below as MessagePaths.
	for _, be := range cs.Proc.BoundaryEvents {
		if be.AttachedToRef == nodeID && be.Message == nil {
			return fmt.Errorf("callActivity %q: timer/error boundary events are not supported", nodeID)
		}
	}

	processID := ""
	if ca.ExtensionElements.ZeebeCalledElement != nil {
		processID = ca.ExtensionElements.ZeebeCalledElement.ProcessID
	}
	calledProc := bpmncore.FindProcessByID(cs.Defs, processID)
	if calledProc == nil {
		return fmt.Errorf("callActivity %q: called process %q not found", nodeID, processID)
	}

	deptsRemap := extractDeptsMap(ca.ExtensionElements.IOMapping)
	steps, err := compileCalledProcess(calledProc, ca.ExtensionElements.IOMapping, deptsRemap, cs)
	if err != nil {
		return fmt.Errorf("callActivity %q: %w", nodeID, err)
	}

	// Flatten called-process steps directly into the parent execution plan.
	cs.FlushSeqBuf()
	firstStepIdx := cs.StepsCount()
	appendStepsWithIOMapping(steps, toIOMapping(ca.ExtensionElements.IOMapping), cs)

	if props := bpmncore.PropsMap(ca.ExtensionElements); len(props) > 0 {
		cs.PatchStep(firstStepIdx, func(s *dsl.ExecutionStep) { s.Extras = props })
	}

	if msgPaths := collectMessagePaths(nodeID, cs); len(msgPaths) > 0 {
		cs.PatchStep(firstStepIdx, func(s *dsl.ExecutionStep) { s.MessagePaths = msgPaths })
	}

	fwd := cs.ForwardNexts(nodeID)
	if len(fwd) > 0 {
		return cs.TraverseNode(fwd[0])
	}
	return nil
}

func collectMessagePaths(nodeID string, cs *bpmncore.CompileState) []dsl.MessagePath {
	var paths []dsl.MessagePath
	for _, be := range cs.Proc.BoundaryEvents {
		if be.AttachedToRef != nodeID || be.Message == nil {
			continue
		}
		msgName := bpmncore.ResolveMessageName(be.Message.MessageRef, cs.Defs)
		if msgName == "" {
			msgName = bpmncore.ResolveMessageFlowTarget(be.ID, cs.Defs)
		}
		targetDept := ""
		if nexts := cs.ForwardNexts(be.ID); len(nexts) > 0 {
			targetDept, _, _ = cs.DeptOf(nexts[0])
		}
		paths = append(paths, dsl.MessagePath{
			MessageName:  msgName,
			Interrupting: be.CancelActivity != "false",
			TargetDept:   targetDept,
		})
	}
	return paths
}

func compileCalledProcess(calledProc *bpmncore.BPMNProcess, ioMapping *bpmncore.ZeebeIOMapping, deptsRemap map[string]string, cs *bpmncore.CompileState) ([]dsl.ExecutionStep, error) {
	innerG := bpmncore.BuildGraph(calledProc)
	innerLaneLabels := bpmncore.BuildLaneLabels(calledProc)
	innerCondExprs := bpmncore.BuildCondExprs(calledProc)
	innerBackEdges := map[[2]string]bool{}
	if len(calledProc.StartEvents) > 0 {
		innerBackEdges = bpmncore.ClassifyBackEdges(innerG, calledProc.StartEvents[0].ID)
	}

	innerState := bpmncore.NewCompileState(calledProc, innerG, innerLaneLabels, innerCondExprs, innerBackEdges, cs.StageTypes, cs.Elements, cs.Defs)
	if len(calledProc.StartEvents) > 0 {
		nexts := innerState.ForwardNexts(calledProc.StartEvents[0].ID)
		if len(nexts) > 0 {
			if err := innerState.TraverseNode(nexts[0]); err != nil {
				return nil, err
			}
		}
	}

	if err := innerState.CompileBoundaryPaths(); err != nil {
		return nil, err
	}
	mergeDepts(innerState.CollectedDepts(), calledProc, ioMapping, deptsRemap, cs)
	steps := innerState.CollectedSteps()
	if len(calledProc.LaneSet.Lanes) == 0 {
		patchEmptyDepts(steps, deptIDFromIOMapping(ioMapping))
	} else if len(deptsRemap) > 0 {
		bpmncore.QualifySteps(steps, func(deptID string) string {
			if deptID == "" {
				return ""
			}
			if mapped, ok := deptsRemap[strings.ToLower(deptID)]; ok && mapped != "" {
				return mapped
			}
			return deptID
		})
	}
	return steps, nil
}

func mergeDepts(depts []dsl.DepartmentDef, calledProc *bpmncore.BPMNProcess, ioMapping *bpmncore.ZeebeIOMapping, deptsRemap map[string]string, cs *bpmncore.CompileState) {
	if len(calledProc.LaneSet.Lanes) == 0 {
		mergeLanelessDepts(depts, deptIDFromIOMapping(ioMapping), cs)
		return
	}
	deptsByID := make(map[string]dsl.DepartmentDef, len(depts))
	for _, d := range depts {
		deptsByID[d.ID] = d
	}
	for _, lane := range calledProc.LaneSet.Lanes {
		dept, ok := deptsByID[lane.Name]
		if !ok {
			continue
		}
		deptID := lane.Name
		if mapped, ok := deptsRemap[strings.ToLower(lane.Name)]; ok && mapped != "" {
			deptID = mapped
		}
		props := bpmncore.PropsMap(lane.ExtensionElements)
		ignore := props["ignore"] == "true"
		cs.EnsureDept(deptID, deptID, "")
		cs.SetDeptMeta(deptID, ignore, props)
		for _, stage := range dept.Stages {
			cs.AppendStage(deptID, stage)
		}
	}
}

func extractDeptsMap(m *bpmncore.ZeebeIOMapping) map[string]string {
	if m == nil {
		return nil
	}
	for _, in := range m.Inputs {
		if in.Target != "Depts" {
			continue
		}
		var result map[string]string
		if err := json.Unmarshal([]byte(in.Source), &result); err != nil {
			return nil
		}
		lower := make(map[string]string, len(result))
		for k, v := range result {
			lower[strings.ToLower(k)] = v
		}
		return lower
	}
	return nil
}

func mergeLanelessDepts(depts []dsl.DepartmentDef, deptID string, cs *bpmncore.CompileState) {
	if deptID == "" {
		return
	}
	cs.EnsureDept(deptID, deptID, "")
	for _, dept := range depts {
		for _, stage := range dept.Stages {
			cs.AppendStage(deptID, stage)
		}
	}
}

func appendStepsWithIOMapping(steps []dsl.ExecutionStep, ioMap *dsl.IOMapping, cs *bpmncore.CompileState) {
	for i, step := range steps {
		if i == 0 {
			step.IOMapping = ioMap
		}
		cs.AppendStep(step)
	}
}

func deptIDFromIOMapping(m *bpmncore.ZeebeIOMapping) string {
	if m == nil {
		return ""
	}
	for _, in := range m.Inputs {
		if in.Target == "dept_id" {
			return in.Source
		}
	}
	return ""
}

func toIOMapping(m *bpmncore.ZeebeIOMapping) *dsl.IOMapping {
	if m == nil {
		return nil
	}
	result := &dsl.IOMapping{}
	for _, in := range m.Inputs {
		if in.Target != "dept_id" && in.Target != "Depts" { // compiler-internal, not passed to execution
			result.Inputs = append(result.Inputs, dsl.IOVar{Source: in.Source, Target: in.Target})
		}
	}
	for _, out := range m.Outputs {
		result.Outputs = append(result.Outputs, dsl.IOVar{Source: out.Source, Target: out.Target})
	}
	if len(result.Inputs) == 0 && len(result.Outputs) == 0 {
		return nil
	}
	return result
}
