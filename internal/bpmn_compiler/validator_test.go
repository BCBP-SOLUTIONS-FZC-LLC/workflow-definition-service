package bpmn_compiler

import (
	"strings"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/validator"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestValidateCounts_MultipleEndEventsAllowed(t *testing.T) {
	proc := &bpmncore.BPMNProcess{
		StartEvents: []bpmncore.BPMNEvent{{ID: "S1"}},
		EndEvents:   []bpmncore.BPMNEvent{{ID: "E1"}, {ID: "E2"}, {ID: "E3"}},
	}
	errs := validator.ValidateCounts(proc)
	if hasCodeIn(errs, domain.BPMNErrMultipleEndEvents) {
		t.Errorf("multiple end events should be valid; got MULTIPLE_END_EVENTS error")
	}
}

func TestValidateCounts_LaneLimitExceeded(t *testing.T) {
	lanes := make([]bpmncore.BPMNLane, validator.MaxLanes+1)
	for i := range lanes {
		lanes[i] = bpmncore.BPMNLane{ID: "lane", Name: "lane"}
	}
	proc := &bpmncore.BPMNProcess{
		StartEvents: []bpmncore.BPMNEvent{{ID: "S1"}},
		EndEvents:   []bpmncore.BPMNEvent{{ID: "E1"}},
		LaneSet:     bpmncore.BPMNLaneSet{Lanes: lanes},
	}
	errs := validator.ValidateCounts(proc)
	if !hasCodeIn(errs, domain.BPMNErrLaneLimitExceeded) {
		t.Errorf("expected LANE_LIMIT_EXCEEDED; got %v", errCodesOf(errs))
	}
}

func TestValidateCounts_TaskLimitExceeded(t *testing.T) {
	tasks := make([]bpmncore.BPMNUserTask, validator.MaxUserTasks+1)
	proc := &bpmncore.BPMNProcess{
		StartEvents: []bpmncore.BPMNEvent{{ID: "S1"}},
		EndEvents:   []bpmncore.BPMNEvent{{ID: "E1"}},
		UserTasks:   tasks,
	}
	errs := validator.ValidateCounts(proc)
	if !hasCodeIn(errs, domain.BPMNErrTaskLimitExceeded) {
		t.Errorf("expected TASK_LIMIT_EXCEEDED; got %v", errCodesOf(errs))
	}
}

func TestValidateSeqFlowRefs_InvalidSourceRef(t *testing.T) {
	proc := &bpmncore.BPMNProcess{
		SequenceFlows: []bpmncore.BPMNSequenceFlow{
			{ID: "F1", SourceRef: "ghost-node", TargetRef: "S1"},
		},
	}
	g := &bpmncore.Graph{
		NodeIDs: map[string]struct{}{"S1": {}},
	}
	errs := validator.ValidateSeqFlowRefs(proc, g)
	if !hasCodeIn(errs, domain.BPMNErrInvalidSequenceFlowRef) {
		t.Errorf("expected INVALID_SEQUENCE_FLOW_REF for invalid sourceRef; got %v", errCodesOf(errs))
	}
}

func TestValidateSeqFlowRefs_InvalidTargetRef(t *testing.T) {
	proc := &bpmncore.BPMNProcess{
		SequenceFlows: []bpmncore.BPMNSequenceFlow{
			{ID: "F1", SourceRef: "S1", TargetRef: "ghost-node"},
		},
	}
	g := &bpmncore.Graph{
		NodeIDs: map[string]struct{}{"S1": {}},
	}
	errs := validator.ValidateSeqFlowRefs(proc, g)
	if !hasCodeIn(errs, domain.BPMNErrInvalidSequenceFlowRef) {
		t.Errorf("expected INVALID_SEQUENCE_FLOW_REF for invalid targetRef; got %v", errCodesOf(errs))
	}
}

// validateTaskExtensions / new-style struct-field validation

func TestValidateTaskExtensions_MissingTaskDefinition(t *testing.T) {
	proc := &bpmncore.BPMNProcess{
		LaneSet: bpmncore.BPMNLaneSet{Lanes: []bpmncore.BPMNLane{
			{ID: "L1", Name: "Design", FlowNodeRefs: []string{"T1"}},
		}},
		UserTasks: []bpmncore.BPMNUserTask{
			{ID: "T1", ExtensionElements: bpmncore.BPMNExtensionElements{
				AssignmentDefinition: &bpmncore.ZeebeAssignmentDefinition{CandidateGroups: "preparer"},
			}},
		},
	}
	errs := validator.ValidateTaskExtensions(proc, defaultStageTypes())
	if !hasCodeIn(errs, domain.BPMNErrMissingTaskDefinition) {
		t.Errorf("expected MISSING_TASK_DEFINITION; got %v", errCodesOf(errs))
	}
}

func TestValidateTaskExtensions_InvalidTaskDefinitionType(t *testing.T) {
	proc := &bpmncore.BPMNProcess{
		LaneSet: bpmncore.BPMNLaneSet{Lanes: []bpmncore.BPMNLane{
			{ID: "L1", Name: "Design", FlowNodeRefs: []string{"T1"}},
		}},
		UserTasks: []bpmncore.BPMNUserTask{
			{ID: "T1", ExtensionElements: bpmncore.BPMNExtensionElements{
				TaskDefinition:       &bpmncore.ZeebeTaskDefinition{Type: "audit"},
				AssignmentDefinition: &bpmncore.ZeebeAssignmentDefinition{CandidateGroups: "auditor"},
			}},
		},
	}
	errs := validator.ValidateTaskExtensions(proc, defaultStageTypes())
	if !hasCodeIn(errs, domain.BPMNErrInvalidTaskDefinitionType) {
		t.Errorf("expected INVALID_TASK_DEFINITION_TYPE; got %v", errCodesOf(errs))
	}
}

func TestValidateTaskExtensions_MissingAssignmentDefinition(t *testing.T) {
	proc := &bpmncore.BPMNProcess{
		LaneSet: bpmncore.BPMNLaneSet{Lanes: []bpmncore.BPMNLane{
			{ID: "L1", Name: "Design", FlowNodeRefs: []string{"T1"}},
		}},
		UserTasks: []bpmncore.BPMNUserTask{
			{ID: "T1", ExtensionElements: bpmncore.BPMNExtensionElements{
				TaskDefinition: &bpmncore.ZeebeTaskDefinition{Type: "prep"},
			}},
		},
	}
	errs := validator.ValidateTaskExtensions(proc, defaultStageTypes())
	if !hasCodeIn(errs, domain.BPMNErrMissingAssignmentDefinition) {
		t.Errorf("expected MISSING_ASSIGNMENT_DEFINITION; got %v", errCodesOf(errs))
	}
}

func TestValidateTaskExtensions_CandidateGroupsTooLong(t *testing.T) {
	proc := &bpmncore.BPMNProcess{
		LaneSet: bpmncore.BPMNLaneSet{Lanes: []bpmncore.BPMNLane{
			{ID: "L1", Name: "Design", FlowNodeRefs: []string{"T1"}},
		}},
		UserTasks: []bpmncore.BPMNUserTask{
			{ID: "T1", ExtensionElements: bpmncore.BPMNExtensionElements{
				TaskDefinition: &bpmncore.ZeebeTaskDefinition{Type: "prep"},
				AssignmentDefinition: &bpmncore.ZeebeAssignmentDefinition{
					CandidateGroups: strings.Repeat("x", validator.MaxRoleLen+1),
					CandidateUsers:  aliceUUID,
				},
			}},
		},
	}
	errs := validator.ValidateTaskExtensions(proc, defaultStageTypes())
	if !hasCodeIn(errs, domain.BPMNErrCandidateGroupsEmpty) {
		t.Errorf("expected CANDIDATE_GROUPS_EMPTY for over-long candidateGroups; got %v", errCodesOf(errs))
	}
}

func TestValidateTaskExtensions_InvalidCandidateUser(t *testing.T) {
	proc := &bpmncore.BPMNProcess{
		LaneSet: bpmncore.BPMNLaneSet{Lanes: []bpmncore.BPMNLane{
			{ID: "L1", Name: "Design", FlowNodeRefs: []string{"T1"}},
		}},
		UserTasks: []bpmncore.BPMNUserTask{
			{ID: "T1", ExtensionElements: bpmncore.BPMNExtensionElements{
				TaskDefinition: &bpmncore.ZeebeTaskDefinition{Type: "prep"},
				AssignmentDefinition: &bpmncore.ZeebeAssignmentDefinition{
					CandidateGroups: "preparer",
					CandidateUsers:  "not-a-uuid",
				},
			}},
		},
	}
	errs := validator.ValidateTaskExtensions(proc, defaultStageTypes())
	if !hasCodeIn(errs, domain.BPMNErrInvalidCandidateUser) {
		t.Errorf("expected INVALID_CANDIDATE_USER; got %v", errCodesOf(errs))
	}
}

func TestValidateLaneMembership_TaskNotInLane(t *testing.T) {
	refs := map[string]struct{}{"T1": {}, "T2": {}}
	errs := validator.ValidateLaneMembership("T3", refs)
	if !hasCodeIn(errs, domain.BPMNErrTaskNotInLane) {
		t.Errorf("expected TASK_NOT_IN_LANE; got %v", errCodesOf(errs))
	}
}

func TestValidateLaneMembership_TaskInLane(t *testing.T) {
	refs := map[string]struct{}{"T1": {}}
	if errs := validator.ValidateLaneMembership("T1", refs); len(errs) != 0 {
		t.Errorf("expected no errors for task in lane; got %v", errs)
	}
}

func TestBuildLaneRefSet(t *testing.T) {
	proc := &bpmncore.BPMNProcess{
		LaneSet: bpmncore.BPMNLaneSet{Lanes: []bpmncore.BPMNLane{
			{ID: "L1", Name: "Design", FlowNodeRefs: []string{"T1", "S1"}},
			{ID: "L2", Name: "QA", FlowNodeRefs: []string{"T2"}},
		}},
	}
	refs := bpmncore.BuildLaneRefSet(proc)
	for _, id := range []string{"T1", "S1", "T2"} {
		if _, ok := refs[id]; !ok {
			t.Errorf("expected %q in lane ref set", id)
		}
	}
	if _, ok := refs["ghost"]; ok {
		t.Error("ghost should not be in lane ref set")
	}
}

func TestValidateRequiresComment_Invalid(t *testing.T) {
	ext := bpmncore.BPMNExtensionElements{
		ZeebeProps: bpmncore.BPMNZeebeProperties{Items: []bpmncore.ZeebeProperty{
			{Name: "requires_comment", Value: "maybe"},
		}},
	}
	errs := validator.ValidateRequiresComment("T1", ext)
	if len(errs) == 0 {
		t.Error("expected error for invalid requires_comment value")
	}
}

func TestValidateRequiresComment_Valid(t *testing.T) {
	for _, val := range []string{"true", "false"} {
		ext := bpmncore.BPMNExtensionElements{
			ZeebeProps: bpmncore.BPMNZeebeProperties{Items: []bpmncore.ZeebeProperty{
				{Name: "requires_comment", Value: val},
			}},
		}
		if errs := validator.ValidateRequiresComment("T1", ext); len(errs) != 0 {
			t.Errorf("requires_comment=%q should be valid; got %v", val, errs)
		}
	}
}

func TestValidateGatewayMatching_ExclusiveAllBranchesTerminate_Valid(t *testing.T) {
	proc := &bpmncore.BPMNProcess{
		ExclusiveGateways: []bpmncore.BPMNGateway{{ID: "GW_split"}},
	}
	g := &bpmncore.Graph{
		Outgoing: map[string][]string{
			"GW_split": {"TaskA", "TaskB"},
			"TaskA":    {"End1"},
			"TaskB":    {"End2"},
		},
		Incoming: map[string][]string{
			"TaskA": {"GW_split"},
			"TaskB": {"GW_split"},
			"End1":  {"TaskA"},
			"End2":  {"TaskB"},
		},
		NodeType: map[string]bpmncore.FlowNodeType{
			"GW_split": bpmncore.NodeTypeExclusiveGateway,
			"TaskA":    bpmncore.NodeTypeUserTask,
			"TaskB":    bpmncore.NodeTypeUserTask,
			"End1":     bpmncore.NodeTypeEndEvent,
			"End2":     bpmncore.NodeTypeEndEvent,
		},
	}
	errs := validator.ValidateGatewayMatching(proc, g, nil)
	if hasCodeIn(errs, domain.BPMNErrUnmatchedGateway) {
		t.Errorf("expected no UNMATCHED_GATEWAY when all XOR branches terminate; got %v", errCodesOf(errs))
	}
}

func TestValidateGatewayMatching_ExclusiveNoJoinNonTerminating(t *testing.T) {
	proc := &bpmncore.BPMNProcess{
		ExclusiveGateways: []bpmncore.BPMNGateway{{ID: "GW_split"}},
	}
	g := &bpmncore.Graph{
		Outgoing: map[string][]string{
			"GW_split": {"TaskA", "TaskB"},
			"TaskA":    {"TaskC"},
			"TaskB":    {"TaskD"},
			"TaskC":    {},
			"TaskD":    {},
		},
		Incoming: map[string][]string{
			"TaskA": {"GW_split"},
			"TaskB": {"GW_split"},
			"TaskC": {"TaskA"},
			"TaskD": {"TaskB"},
		},
		NodeType: map[string]bpmncore.FlowNodeType{
			"GW_split": bpmncore.NodeTypeExclusiveGateway,
			"TaskA":    bpmncore.NodeTypeUserTask,
			"TaskB":    bpmncore.NodeTypeUserTask,
			"TaskC":    bpmncore.NodeTypeUserTask,
			"TaskD":    bpmncore.NodeTypeUserTask,
		},
	}
	errs := validator.ValidateGatewayMatching(proc, g, nil)
	if !hasCodeIn(errs, domain.BPMNErrUnmatchedGateway) {
		t.Errorf("expected UNMATCHED_GATEWAY when XOR branches don't terminate or rejoin; got %v", errCodesOf(errs))
	}
}

func TestValidateGatewayMatching_ParallelNoJoin(t *testing.T) {
	proc := &bpmncore.BPMNProcess{
		ParallelGateways: []bpmncore.BPMNGateway{{ID: "GW_split"}},
	}
	g := &bpmncore.Graph{
		Outgoing: map[string][]string{
			"GW_split": {"TaskA", "TaskB"},
			"TaskA":    {"End1"},
			"TaskB":    {"End2"},
		},
		Incoming: map[string][]string{
			"TaskA": {"GW_split"},
			"TaskB": {"GW_split"},
			"End1":  {"TaskA"},
			"End2":  {"TaskB"},
		},
		NodeType: map[string]bpmncore.FlowNodeType{
			"GW_split": bpmncore.NodeTypeParallelGateway,
		},
	}
	errs := validator.ValidateGatewayMatching(proc, g, nil)
	if !hasCodeIn(errs, domain.BPMNErrUnmatchedGateway) {
		t.Errorf("expected UNMATCHED_GATEWAY for parallel split; got %v", errCodesOf(errs))
	}
}

func TestValidateGatewayMatching_BranchBypassesJoin(t *testing.T) {
	proc := &bpmncore.BPMNProcess{
		ParallelGateways: []bpmncore.BPMNGateway{{ID: "GW_split"}},
	}
	g := &bpmncore.Graph{
		Outgoing: map[string][]string{
			"GW_split": {"A", "B"},
			"A":        {"J1"},
			"C":        {"J1"},
			"B":        {"End"},
		},
		Incoming: map[string][]string{
			"A":   {"GW_split"},
			"B":   {"GW_split"},
			"J1":  {"A", "C"},
			"End": {"B"},
		},
		NodeType: map[string]bpmncore.FlowNodeType{
			"GW_split": bpmncore.NodeTypeParallelGateway,
		},
	}
	errs := validator.ValidateGatewayMatching(proc, g, map[[2]string]bool{})
	if !hasCodeIn(errs, domain.BPMNErrUnmatchedGateway) {
		t.Errorf("expected UNMATCHED_GATEWAY for non-reconverging branch; got %v", errCodesOf(errs))
	}
}

func TestValidateConditionExprs_TooLong(t *testing.T) {
	longExpr := strings.Repeat("x", validator.MaxCondExpr+1)
	proc := &bpmncore.BPMNProcess{
		SequenceFlows: []bpmncore.BPMNSequenceFlow{
			{ID: "F1", SourceRef: "GW_split", TargetRef: "TaskA", ConditionExpression: longExpr},
		},
	}
	g := &bpmncore.Graph{
		Outgoing: map[string][]string{"GW_split": {"TaskA", "TaskB"}},
	}
	errs := validator.ValidateConditionExprs(proc, g)
	if !hasCodeIn(errs, domain.BPMNErrInvalidConditionExpression) {
		t.Errorf("expected INVALID_CONDITION_EXPRESSION for over-long conditionExpression; got %v", errCodesOf(errs))
	}
}

// CheckDangling tests

func TestCheckDangling_NoIncoming(t *testing.T) {
	g := &bpmncore.Graph{
		Incoming: map[string][]string{},
		Outgoing: map[string][]string{"T1": {"E1"}},
	}
	errs := validator.CheckDangling("T1", g)
	if !hasCodeIn(errs, domain.BPMNErrDanglingNode) {
		t.Errorf("expected DANGLING_NODE for no incoming; got %v", errCodesOf(errs))
	}
}

func TestCheckDangling_NoOutgoing(t *testing.T) {
	g := &bpmncore.Graph{
		Incoming: map[string][]string{"T1": {"S1"}},
		Outgoing: map[string][]string{},
	}
	errs := validator.CheckDangling("T1", g)
	if !hasCodeIn(errs, domain.BPMNErrDanglingNode) {
		t.Errorf("expected DANGLING_NODE for no outgoing; got %v", errCodesOf(errs))
	}
}

func TestCheckDangling_Connected(t *testing.T) {
	g := &bpmncore.Graph{
		Incoming: map[string][]string{"T1": {"S1"}},
		Outgoing: map[string][]string{"T1": {"E1"}},
	}
	if errs := validator.CheckDangling("T1", g); len(errs) != 0 {
		t.Errorf("expected no errors for connected node; got %v", errs)
	}
}

// ValidateDanglingNodes — event-specific branches

func TestValidateDanglingNodes_StartNoOutgoing(t *testing.T) {
	proc := &bpmncore.BPMNProcess{
		StartEvents: []bpmncore.BPMNEvent{{ID: "S1"}},
	}
	g := &bpmncore.Graph{
		Outgoing: map[string][]string{},
		Incoming: map[string][]string{},
	}
	errs := validator.ValidateDanglingNodes(proc, g)
	if !hasCodeIn(errs, domain.BPMNErrDanglingNode) {
		t.Errorf("expected DANGLING_NODE for start event with no outgoing; got %v", errCodesOf(errs))
	}
}

func TestValidateDanglingNodes_EndNoIncoming(t *testing.T) {
	proc := &bpmncore.BPMNProcess{
		EndEvents: []bpmncore.BPMNEvent{{ID: "E1"}},
	}
	g := &bpmncore.Graph{
		Outgoing: map[string][]string{},
		Incoming: map[string][]string{},
	}
	errs := validator.ValidateDanglingNodes(proc, g)
	if !hasCodeIn(errs, domain.BPMNErrDanglingNode) {
		t.Errorf("expected DANGLING_NODE for end event with no incoming; got %v", errCodesOf(errs))
	}
}

// ValidateReachability

func TestValidateReachability_NoStartEvent(t *testing.T) {
	proc := &bpmncore.BPMNProcess{}
	g := &bpmncore.Graph{NodeIDs: map[string]struct{}{"T1": {}}}
	errs := validator.ValidateReachability(proc, g)
	if len(errs) != 0 {
		t.Errorf("expected empty errs when no start event; got %v", errs)
	}
}

func TestValidateReachability_UnreachableNode(t *testing.T) {
	proc := &bpmncore.BPMNProcess{StartEvents: []bpmncore.BPMNEvent{{ID: "S1"}}}
	g := &bpmncore.Graph{
		NodeIDs:  map[string]struct{}{"S1": {}, "orphan": {}},
		Outgoing: map[string][]string{"S1": {}},
	}
	errs := validator.ValidateReachability(proc, g)
	if !hasCodeIn(errs, domain.BPMNErrUnreachableNode) {
		t.Errorf("expected UNREACHABLE_NODE; got %v", errCodesOf(errs))
	}
}

func TestValidateReachability_AllReachable(t *testing.T) {
	proc := &bpmncore.BPMNProcess{StartEvents: []bpmncore.BPMNEvent{{ID: "S1"}}}
	g := &bpmncore.Graph{
		NodeIDs:  map[string]struct{}{"S1": {}, "T1": {}, "E1": {}},
		Outgoing: map[string][]string{"S1": {"T1"}, "T1": {"E1"}},
	}
	if errs := validator.ValidateReachability(proc, g); len(errs) != 0 {
		t.Errorf("expected no errors when all nodes reachable; got %v", errs)
	}
}

func TestValidateLoops_NoLoop(t *testing.T) {
	g := &bpmncore.Graph{
		NodeIDs:  map[string]struct{}{"S1": {}, "T1": {}, "E1": {}},
		Outgoing: map[string][]string{"S1": {"T1"}, "T1": {"E1"}},
		Incoming: map[string][]string{"T1": {"S1"}, "E1": {"T1"}},
		NodeType: map[string]bpmncore.FlowNodeType{"S1": bpmncore.NodeTypeStartEvent, "T1": bpmncore.NodeTypeUserTask, "E1": bpmncore.NodeTypeEndEvent},
	}
	if errs := validator.ValidateLoops(g, bpmncore.ClassifyBackEdges(g, "S1")); len(errs) != 0 {
		t.Errorf("expected no errors for acyclic graph; got %v", errCodesOf(errs))
	}
}

func TestValidateLoops_GuardedLoop(t *testing.T) {
	g := &bpmncore.Graph{
		NodeIDs:  map[string]struct{}{"S1": {}, "T1": {}, "G": {}, "E1": {}},
		Outgoing: map[string][]string{"S1": {"T1"}, "T1": {"G"}, "G": {"E1", "T1"}},
		Incoming: map[string][]string{"T1": {"S1", "G"}, "G": {"T1"}, "E1": {"G"}},
		NodeType: map[string]bpmncore.FlowNodeType{"S1": bpmncore.NodeTypeStartEvent, "T1": bpmncore.NodeTypeUserTask, "G": bpmncore.NodeTypeExclusiveGateway, "E1": bpmncore.NodeTypeEndEvent},
	}
	if errs := validator.ValidateLoops(g, bpmncore.ClassifyBackEdges(g, "S1")); len(errs) != 0 {
		t.Errorf("guarded loop should be valid; got %v", errCodesOf(errs))
	}
}

func TestValidateLoops_UnguardedBackEdgeFromTask(t *testing.T) {
	g := &bpmncore.Graph{
		NodeIDs:  map[string]struct{}{"T1": {}, "T2": {}},
		Outgoing: map[string][]string{"T1": {"T2"}, "T2": {"T1"}},
		Incoming: map[string][]string{"T1": {"T2"}, "T2": {"T1"}},
		NodeType: map[string]bpmncore.FlowNodeType{"T1": bpmncore.NodeTypeUserTask, "T2": bpmncore.NodeTypeUserTask},
	}
	errs := validator.ValidateLoops(g, bpmncore.ClassifyBackEdges(g, "T1"))
	if !hasCodeIn(errs, domain.BPMNErrUnguardedLoop) {
		t.Errorf("expected UNGUARDED_LOOP; got %v", errCodesOf(errs))
	}
}

func TestValidateLoops_SelfEdge(t *testing.T) {
	g := &bpmncore.Graph{
		NodeIDs:  map[string]struct{}{"T1": {}},
		Outgoing: map[string][]string{"T1": {"T1"}},
		Incoming: map[string][]string{"T1": {"T1"}},
		NodeType: map[string]bpmncore.FlowNodeType{"T1": bpmncore.NodeTypeUserTask},
	}
	errs := validator.ValidateLoops(g, bpmncore.ClassifyBackEdges(g, "T1"))
	if !hasCodeIn(errs, domain.BPMNErrUnguardedLoop) {
		t.Errorf("expected UNGUARDED_LOOP for self-edge; got %v", errCodesOf(errs))
	}
}

func TestValidateGatewayMatching_PassthroughGateway(t *testing.T) {
	proc := &bpmncore.BPMNProcess{
		ParallelGateways: []bpmncore.BPMNGateway{{ID: "GW_pass"}},
	}
	g := &bpmncore.Graph{
		Outgoing: map[string][]string{"GW_pass": {"T1"}},
	}
	errs := validator.ValidateGatewayMatching(proc, g, nil)
	if len(errs) != 0 {
		t.Errorf("passthrough gateway (1 outgoing) should produce no errors; got %v", errs)
	}
}

func TestValidateConditionExprs_SingleOutflow_Skipped(t *testing.T) {
	proc := &bpmncore.BPMNProcess{
		SequenceFlows: []bpmncore.BPMNSequenceFlow{
			{ID: "F1", SourceRef: "T1", TargetRef: "E1", ConditionExpression: strings.Repeat("x", validator.MaxCondExpr+1)},
		},
	}
	g := &bpmncore.Graph{Outgoing: map[string][]string{"T1": {"E1"}}}
	if errs := validator.ValidateConditionExprs(proc, g); len(errs) != 0 {
		t.Errorf("expected no error for single-outflow source; got %v", errs)
	}
}

func TestValidateConditionExprs_NoConditionExpression(t *testing.T) {
	proc := &bpmncore.BPMNProcess{
		SequenceFlows: []bpmncore.BPMNSequenceFlow{
			{ID: "F1", SourceRef: "GW1", TargetRef: "T1"},
		},
	}
	g := &bpmncore.Graph{Outgoing: map[string][]string{"GW1": {"T1", "T2"}}}
	if errs := validator.ValidateConditionExprs(proc, g); len(errs) != 0 {
		t.Errorf("expected no error when conditionExpression is absent; got %v", errs)
	}
}

// IsValidDuration

func TestIsValidDuration_GoDurationValid(t *testing.T) {
	for _, s := range []string{"72h", "3h30m", "30m", "1h", "24h", "1h30m45s"} {
		if !validator.IsValidDuration(s) {
			t.Errorf("expected %q to be valid", s)
		}
	}
}

func TestIsValidDuration_ISO8601Valid(t *testing.T) {
	for _, s := range []string{"P3D", "PT72H", "P1DT12H30M", "PT30M", "PT1S", "P7D", "P1DT1H"} {
		if !validator.IsValidDuration(s) {
			t.Errorf("expected %q to be valid ISO 8601 duration", s)
		}
	}
}

func TestIsValidDuration_EmptyRejected(t *testing.T) {
	if validator.IsValidDuration("") {
		t.Error("expected empty string to be rejected")
	}
}

func TestIsValidDuration_MonthsYearsRejected(t *testing.T) {
	for _, s := range []string{"P1M", "P1Y", "P1Y2M3D", "P2Y6M"} {
		if validator.IsValidDuration(s) {
			t.Errorf("expected %q (months/years) to be rejected", s)
		}
	}
}

func TestIsValidDuration_InvalidStringsRejected(t *testing.T) {
	for _, s := range []string{"not-a-duration", "3 hours", "72", "P", "PT"} {
		if validator.IsValidDuration(s) {
			t.Errorf("expected %q to be rejected", s)
		}
	}
}

// ValidateBoundaryEvents

func TestValidateBoundaryEvents_DanglingAttachedToRef(t *testing.T) {
	proc := &bpmncore.BPMNProcess{
		BoundaryEvents: []bpmncore.BPMNBoundaryEvent{
			{ID: "BE_1", AttachedToRef: "ghost_node", Timer: &bpmncore.BPMNTimerDef{Duration: "72h"}},
		},
	}
	g := &bpmncore.Graph{NodeIDs: map[string]struct{}{"Task_1": {}}}
	errs := validator.ValidateBoundaryEvents(proc, g)
	if !hasCodeIn(errs, domain.BPMNErrDanglingNode) {
		t.Errorf("expected DANGLING_NODE for unknown attachedToRef; got %v", errCodesOf(errs))
	}
}

func TestValidateBoundaryEvents_NoDefinition(t *testing.T) {
	proc := &bpmncore.BPMNProcess{
		BoundaryEvents: []bpmncore.BPMNBoundaryEvent{
			{ID: "BE_1", AttachedToRef: "Task_1"},
		},
	}
	g := &bpmncore.Graph{NodeIDs: map[string]struct{}{"Task_1": {}}}
	errs := validator.ValidateBoundaryEvents(proc, g)
	if !hasCodeIn(errs, domain.BPMNErrMissingTaskDefinition) {
		t.Errorf("expected MISSING_TASK_DEFINITION for boundary event with no definition; got %v", errCodesOf(errs))
	}
}

func TestValidateBoundaryEvents_BothTimerAndError(t *testing.T) {
	proc := &bpmncore.BPMNProcess{
		BoundaryEvents: []bpmncore.BPMNBoundaryEvent{
			{ID: "BE_1", AttachedToRef: "Sub_1", Timer: &bpmncore.BPMNTimerDef{Duration: "72h"}, Error: &bpmncore.BPMNErrorDef{}},
		},
	}
	g := &bpmncore.Graph{
		NodeIDs:  map[string]struct{}{"Sub_1": {}},
		NodeType: map[string]bpmncore.FlowNodeType{"Sub_1": bpmncore.NodeTypeSubProcess},
	}
	errs := validator.ValidateBoundaryEvents(proc, g)
	if !hasCodeIn(errs, domain.BPMNErrInvalidBoundaryAttachment) {
		t.Errorf("expected INVALID_BOUNDARY_ATTACHMENT for both timer and error; got %v", errCodesOf(errs))
	}
}

func TestValidateBoundaryEvents_InvalidSLADuration(t *testing.T) {
	proc := &bpmncore.BPMNProcess{
		BoundaryEvents: []bpmncore.BPMNBoundaryEvent{
			{ID: "BE_1", AttachedToRef: "Task_1", Timer: &bpmncore.BPMNTimerDef{Duration: "not-a-duration"}},
		},
	}
	g := &bpmncore.Graph{
		NodeIDs:  map[string]struct{}{"Task_1": {}},
		NodeType: map[string]bpmncore.FlowNodeType{"Task_1": bpmncore.NodeTypeUserTask},
	}
	errs := validator.ValidateBoundaryEvents(proc, g)
	if !hasCodeIn(errs, domain.BPMNErrInvalidSLADuration) {
		t.Errorf("expected INVALID_SLA_DURATION; got %v", errCodesOf(errs))
	}
}

func TestValidateBoundaryEvents_MultipleTimersOnSameHost(t *testing.T) {
	proc := &bpmncore.BPMNProcess{
		BoundaryEvents: []bpmncore.BPMNBoundaryEvent{
			{ID: "BE_1", AttachedToRef: "Task_1", Timer: &bpmncore.BPMNTimerDef{Duration: "72h"}},
			{ID: "BE_2", AttachedToRef: "Task_1", Timer: &bpmncore.BPMNTimerDef{Duration: "24h"}},
		},
	}
	g := &bpmncore.Graph{
		NodeIDs:  map[string]struct{}{"Task_1": {}},
		NodeType: map[string]bpmncore.FlowNodeType{"Task_1": bpmncore.NodeTypeUserTask},
	}
	errs := validator.ValidateBoundaryEvents(proc, g)
	if !hasCodeIn(errs, domain.BPMNErrInvalidBoundaryAttachment) {
		t.Errorf("expected INVALID_BOUNDARY_ATTACHMENT for duplicate timer on same host; got %v", errCodesOf(errs))
	}
}

func TestValidateBoundaryEvents_ErrorBoundaryOnUserTask(t *testing.T) {
	proc := &bpmncore.BPMNProcess{
		BoundaryEvents: []bpmncore.BPMNBoundaryEvent{
			{ID: "BE_1", AttachedToRef: "Task_1", Error: &bpmncore.BPMNErrorDef{ErrorRef: "Error_1"}},
		},
	}
	g := &bpmncore.Graph{
		NodeIDs:  map[string]struct{}{"Task_1": {}},
		NodeType: map[string]bpmncore.FlowNodeType{"Task_1": bpmncore.NodeTypeUserTask},
	}
	errs := validator.ValidateBoundaryEvents(proc, g)
	if !hasCodeIn(errs, domain.BPMNErrInvalidBoundaryAttachment) {
		t.Errorf("expected INVALID_BOUNDARY_ATTACHMENT for error boundary on userTask; got %v", errCodesOf(errs))
	}
}

func TestValidateBoundaryEvents_ValidTimerOnTask(t *testing.T) {
	proc := &bpmncore.BPMNProcess{
		BoundaryEvents: []bpmncore.BPMNBoundaryEvent{
			{ID: "BE_1", AttachedToRef: "Task_1", Timer: &bpmncore.BPMNTimerDef{Duration: "72h"}},
		},
	}
	g := &bpmncore.Graph{
		NodeIDs:  map[string]struct{}{"Task_1": {}},
		NodeType: map[string]bpmncore.FlowNodeType{"Task_1": bpmncore.NodeTypeUserTask},
	}
	if errs := validator.ValidateBoundaryEvents(proc, g); len(errs) != 0 {
		t.Errorf("expected no errors for valid timer boundary on task; got %v", errs)
	}
}

func TestValidateBoundaryEvents_ValidErrorOnSubProcess(t *testing.T) {
	proc := &bpmncore.BPMNProcess{
		BoundaryEvents: []bpmncore.BPMNBoundaryEvent{
			{ID: "BE_1", AttachedToRef: "Sub_1", Error: &bpmncore.BPMNErrorDef{ErrorRef: "Error_1"}},
		},
	}
	g := &bpmncore.Graph{
		NodeIDs:  map[string]struct{}{"Sub_1": {}},
		NodeType: map[string]bpmncore.FlowNodeType{"Sub_1": bpmncore.NodeTypeSubProcess},
	}
	if errs := validator.ValidateBoundaryEvents(proc, g); len(errs) != 0 {
		t.Errorf("expected no errors for valid error boundary on subprocess; got %v", errs)
	}
}

func TestValidateDiagramCompleteness_MissingDiagram(t *testing.T) {
	proc := &bpmncore.BPMNProcess{StartEvents: []bpmncore.BPMNEvent{{ID: "S1"}}}
	defs := &bpmncore.BPMNDefinitions{} // no Diagrams
	errs := validator.ValidateDiagramCompleteness(proc, defs)
	if !hasCodeIn(errs, domain.BPMNErrMissingDiagram) {
		t.Errorf("expected BPMNErrMissingDiagram when Diagrams is empty; got %v", errCodesOf(errs))
	}
	if len(errs) != 1 {
		t.Errorf("expected exactly 1 error; got %d", len(errs))
	}
}

func hasCodeIn(errs []domain.BPMNValidationError, code domain.BPMNErrorCode) bool {
	for _, e := range errs {
		if e.Code == code {
			return true
		}
	}
	return false
}

func errCodesOf(errs []domain.BPMNValidationError) []domain.BPMNErrorCode {
	codes := make([]domain.BPMNErrorCode, len(errs))
	for i, e := range errs {
		codes[i] = e.Code
	}
	return codes
}
