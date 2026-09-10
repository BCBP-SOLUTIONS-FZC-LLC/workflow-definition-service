package bpmncore

import (
	"fmt"
	"strings"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/pkg/dsl"
)

func Compile(proc *BPMNProcess, g *Graph, stageTypes map[string]StageTypeHandler, elements map[FlowNodeType]ElementHandler, defs *BPMNDefinitions) (*dsl.CompiledPlan, error) {
	implicit := ""
	if len(proc.StartEvents) == 0 {
		implicit = FindImplicitStart(proc, g)
	}
	return CompileWithImplicitStart(proc, g, implicit, stageTypes, elements, defs)
}

func CompileWithImplicitStart(proc *BPMNProcess, g *Graph, implicitStart string, stageTypes map[string]StageTypeHandler, elements map[FlowNodeType]ElementHandler, defs *BPMNDefinitions, externalNodes ...map[string]string) (*dsl.CompiledPlan, error) {
	startID := ""
	if len(proc.StartEvents) > 0 {
		startID = proc.StartEvents[0].ID
	} else {
		startID = implicitStart
	}
	if startID == "" {
		return nil, fmt.Errorf("compile: no start event")
	}

	laneLabels := BuildLaneLabels(proc)
	condExprs := BuildCondExprs(proc)
	backEdges := ClassifyBackEdges(g, startID)

	state := NewCompileState(proc, g, laneLabels, condExprs, backEdges, stageTypes, elements, defs)
	if len(externalNodes) > 0 {
		for k, v := range externalNodes[0] {
			state.ExternalNodes[k] = v
		}
	}
	// An implicit-start node (e.g. a receiveTask) is itself the first real
	// task, unlike an explicit <bpmn:startEvent> whose successor is.
	if len(proc.StartEvents) > 0 {
		nexts := state.ForwardNexts(startID)
		if len(nexts) > 0 {
			if err := state.TraverseNode(nexts[0]); err != nil {
				return nil, fmt.Errorf("compile: %w", err)
			}
		}
	} else {
		if err := state.TraverseNode(startID); err != nil {
			return nil, fmt.Errorf("compile: %w", err)
		}
	}
	if err := state.CompileBoundaryPaths(); err != nil {
		return nil, fmt.Errorf("compile: %w", err)
	}

	var visualElems []dsl.VisualElementDef
	for _, ds := range proc.DataStoreRefs {
		visualElems = append(visualElems, dsl.VisualElementDef{
			Kind: "dataStoreReference",
			ID:   ds.ID,
			Name: ds.Name,
		})
	}

	return &dsl.CompiledPlan{
		Name:           proc.Name,
		TaskQueue:      defaultTaskQueue,
		Departments:    state.CollectedDepts(),
		Execution:      dsl.ExecutionPlan{Steps: state.steps},
		VisualElements: visualElems,
	}, nil
}

func BuildStageDef(task *BPMNUserTask, stageTypes map[string]StageTypeHandler, proc *BPMNProcess, defs *BPMNDefinitions) (dsl.StageDef, error) {
	ext := task.ExtensionElements

	if connectorType, ok := ConnectorType(ext); ok {
		return buildConnectorStageDef(task, connectorType, proc, defs), nil
	}

	stageType := ""
	if ext.TaskDefinition != nil {
		stageType = ext.TaskDefinition.Type
	}
	h, known := stageTypes[stageType]

	role := ""
	var assignees []string
	if ext.AssignmentDefinition != nil {
		role = ext.AssignmentDefinition.CandidateGroups
		if cu := strings.TrimSpace(ext.AssignmentDefinition.CandidateUsers); cu != "" {
			assignees = []string{cu}
		}
	}

	extras := map[string]string{}
	for _, p := range ext.ZeebeProps.Items {
		extras[p.Name] = p.Value
	}

	activityName := stageType
	if known {
		activityName = h.ActivityName()
	}

	engineNote := ""
	if !known {
		engineNote = fmt.Sprintf("stage type %q is not a defined class in the workflow engine", stageType)
	}

	var extrasOut map[string]string
	if len(extras) > 0 {
		extrasOut = extras
	}

	stage := dsl.StageDef{
		Type:             stageType,
		Activity:         activityName,
		NodeID:           task.ID,
		Role:             role,
		DefaultAssignees: assignees,
		EngineNote:       engineNote,
		Extras:           extrasOut,
		IsZeebeUserTask:  ext.ZeebeUserTask != nil,
	}

	if be := TimerBoundaryFor(task.ID, proc); be != nil {
		stage.BoundaryTimer = &dsl.BoundaryTimer{
			Duration:     be.Timer.Duration,
			Interrupting: be.CancelActivity != "false",
		}
	}

	if be := MessageBoundaryFor(task.ID, proc); be != nil {
		msgName := ResolveMessageName(be.Message.MessageRef, defs)
		if msgName == "" {
			msgName = ResolveMessageFlowTarget(be.ID, defs)
		}
		stage.BoundaryMessage = &dsl.MessagePath{
			MessageName:  msgName,
			Interrupting: be.CancelActivity != "false",
		}
	}

	if ts := ext.TaskSchedule; ts != nil {
		stage.DueDate = ts.DueDate
		stage.FollowUpDate = ts.FollowUpDate
	}

	return stage, nil
}

// buildConnectorStageDef builds a connector-typed StageDef (design/LLD/workflow_connectors.md
// §3/§5.1) — no assignee/role fields (connector tasks are fully automation-only)
// and IOMapping copied unfiltered from the parsed zeebe:ioMapping.
func buildConnectorStageDef(task *BPMNUserTask, connectorType string, proc *BPMNProcess, defs *BPMNDefinitions) dsl.StageDef {
	ext := task.ExtensionElements

	activityName := task.Name
	if activityName == "" {
		activityName = connectorType
	}

	extras := map[string]string{}
	for _, p := range ext.ZeebeProps.Items {
		extras[p.Name] = p.Value
	}
	var extrasOut map[string]string
	if len(extras) > 0 {
		extrasOut = extras
	}

	stage := dsl.StageDef{
		Type:          "connector",
		Activity:      activityName,
		NodeID:        task.ID,
		ConnectorType: connectorType,
		IOMapping:     toConnectorIOMapping(ext.IOMapping),
		Extras:        extrasOut,
	}

	if be := TimerBoundaryFor(task.ID, proc); be != nil {
		stage.BoundaryTimer = &dsl.BoundaryTimer{
			Duration:     be.Timer.Duration,
			Interrupting: be.CancelActivity != "false",
		}
	}
	if be := MessageBoundaryFor(task.ID, proc); be != nil {
		msgName := ResolveMessageName(be.Message.MessageRef, defs)
		if msgName == "" {
			msgName = ResolveMessageFlowTarget(be.ID, defs)
		}
		stage.BoundaryMessage = &dsl.MessagePath{
			MessageName:  msgName,
			Interrupting: be.CancelActivity != "false",
		}
	}
	if ts := ext.TaskSchedule; ts != nil {
		stage.DueDate = ts.DueDate
		stage.FollowUpDate = ts.FollowUpDate
	}

	return stage
}

// toConnectorIOMapping copies every input/output unfiltered, unlike
// element/call_activity.go's toIOMapping which strips dept_id/Depts targets —
// those are callActivity-only compiler-internal conventions that don't apply here.
func toConnectorIOMapping(m *ZeebeIOMapping) *dsl.IOMapping {
	if m == nil {
		return nil
	}
	result := &dsl.IOMapping{}
	for _, in := range m.Inputs {
		result.Inputs = append(result.Inputs, dsl.IOVar{Source: in.Source, Target: in.Target})
	}
	for _, out := range m.Outputs {
		result.Outputs = append(result.Outputs, dsl.IOVar{Source: out.Source, Target: out.Target})
	}
	return result
}

func BuildMessageStageDef(taskID, taskName, stageType, messageName string, ext BPMNExtensionElements) dsl.StageDef {
	role := ""
	var assignees []string
	if ext.AssignmentDefinition != nil {
		role = ext.AssignmentDefinition.CandidateGroups
		if cu := strings.TrimSpace(ext.AssignmentDefinition.CandidateUsers); cu != "" {
			assignees = []string{cu}
		}
	}

	extras := map[string]string{}
	for _, p := range ext.ZeebeProps.Items {
		extras[p.Name] = p.Value
	}
	if messageName != "" {
		extras["message"] = messageName
	}
	var extrasOut map[string]string
	if len(extras) > 0 {
		extrasOut = extras
	}

	return dsl.StageDef{
		Type:             stageType,
		Activity:         taskName,
		NodeID:           taskID,
		Role:             role,
		DefaultAssignees: assignees,
		Extras:           extrasOut,
	}
}

func ResolveMessageName(messageRef string, defs *BPMNDefinitions) string {
	if messageRef == "" || defs == nil {
		return ""
	}
	for _, m := range defs.Messages {
		if m.ID == messageRef {
			return m.Name
		}
	}
	return ""
}

func nodeMessageRef(nodeID string, defs *BPMNDefinitions) string {
	if nodeID == "" || defs == nil {
		return ""
	}
	for _, p := range defs.Processes {
		for _, t := range p.SendTasks {
			if t.ID == nodeID {
				return t.MessageRef
			}
		}
		for _, t := range p.ReceiveTasks {
			if t.ID == nodeID {
				return t.MessageRef
			}
		}
		for _, be := range p.BoundaryEvents {
			if be.ID == nodeID && be.Message != nil {
				return be.Message.MessageRef
			}
		}
	}
	return ""
}

// ResolveMessageFlowName resolves the display name for a message flow,
// trying (in order) the flow's own name/messageRef, then the messageRef
// carried by the flow's target node, then its source node. Real-world BPMN
// diagrams rarely annotate the messageFlow element itself — the messageRef
// almost always lives on the connected send/receive task or boundary event.
func ResolveMessageFlowName(mf *BPMNMessageFlow, defs *BPMNDefinitions) string {
	if mf.Name != "" {
		return mf.Name
	}
	if name := ResolveMessageName(mf.MessageRef, defs); name != "" {
		return name
	}
	if name := ResolveMessageName(nodeMessageRef(mf.TargetRef, defs), defs); name != "" {
		return name
	}
	return ResolveMessageName(nodeMessageRef(mf.SourceRef, defs), defs)
}

// ResolveMessageFlowTarget resolves a message name for a node (typically a
// boundary event) by scanning collaboration message flows whose target is
// that node ID and applying ResolveMessageFlowName.
func ResolveMessageFlowTarget(nodeID string, defs *BPMNDefinitions) string {
	if defs == nil || defs.Collaboration == nil {
		return ""
	}
	for i := range defs.Collaboration.MessageFlows {
		mf := &defs.Collaboration.MessageFlows[i]
		if mf.TargetRef != nodeID {
			continue
		}
		return ResolveMessageFlowName(mf, defs)
	}
	return ""
}
