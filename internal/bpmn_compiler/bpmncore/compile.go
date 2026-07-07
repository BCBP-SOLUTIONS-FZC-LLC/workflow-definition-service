package bpmncore

import (
	"fmt"
	"strings"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func Compile(proc *BPMNProcess, g *Graph, stageTypes map[string]StageTypeHandler, elements map[FlowNodeType]ElementHandler, defs *BPMNDefinitions) (*domain.CompiledPlan, error) {
	implicit := ""
	if len(proc.StartEvents) == 0 {
		implicit = FindImplicitStart(proc, g)
	}
	return CompileWithImplicitStart(proc, g, implicit, stageTypes, elements, defs)
}

func CompileWithImplicitStart(proc *BPMNProcess, g *Graph, implicitStart string, stageTypes map[string]StageTypeHandler, elements map[FlowNodeType]ElementHandler, defs *BPMNDefinitions, externalNodes ...map[string]string) (*domain.CompiledPlan, error) {
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
	// For implicit starts the start node itself is a real task (e.g. receiveTask),
	// so traverse it directly rather than its successors.
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

	var visualElems []domain.VisualElementDef
	for _, ds := range proc.DataStoreRefs {
		visualElems = append(visualElems, domain.VisualElementDef{
			Kind: "dataStoreReference",
			ID:   ds.ID,
			Name: ds.Name,
		})
	}

	return &domain.CompiledPlan{
		Name:           proc.Name,
		TaskQueue:      defaultTaskQueue,
		Departments:    state.CollectedDepts(),
		Execution:      domain.ExecutionPlan{Steps: state.steps},
		VisualElements: visualElems,
	}, nil
}

func BuildStageDef(task *BPMNUserTask, stageTypes map[string]StageTypeHandler, proc *BPMNProcess, defs *BPMNDefinitions) (domain.StageDef, error) {
	ext := task.ExtensionElements

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

	stage := domain.StageDef{
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
		stage.BoundaryTimer = &domain.BoundaryTimer{
			Duration:     be.Timer.Duration,
			Interrupting: be.CancelActivity != "false",
		}
	}

	if be := MessageBoundaryFor(task.ID, proc); be != nil {
		msgName := ResolveMessageName(be.Message.MessageRef, defs)
		if msgName == "" {
			msgName = ResolveMessageFlowTarget(be.ID, defs)
		}
		stage.BoundaryMessage = &domain.MessagePath{
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

func BuildMessageStageDef(taskID, taskName, stageType, messageName string, ext BPMNExtensionElements) domain.StageDef {
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

	return domain.StageDef{
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

// nodeMessageRef returns the messageRef carried by the node identified by
// nodeID, checking send/receive tasks and message boundary events — the
// places BPMN authors actually attach a messageRef, as opposed to the
// <bpmn:messageFlow> element itself which is rarely annotated in practice.
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
