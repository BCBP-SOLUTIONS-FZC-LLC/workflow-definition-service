package bpmn_compiler

import (
	"strings"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestIsValidSLADuration(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		// Go duration syntax
		{"24h", true},
		{"90m", true},
		{"3h30m", true},
		{"1s", true},
		{"0s", false},  // zero duration rejected
		{"-1h", false}, // negative duration rejected
		{"", false},
		{"24", false},
		{"2 hours", false},
		{"2 days", false},
		// day-suffix shorthand
		{"1d", true},
		{"2d", true},
		{"30d", true},
		{"0d", false},   // zero rejected
		{"-1d", false},  // negative rejected
		{"xd", false},   // non-numeric prefix rejected
		{"1.5d", false}, // float rejected
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := isValidSLADuration(tt.input); got != tt.want {
				t.Errorf("isValidSLADuration(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidateCounts_MultipleEndEvents(t *testing.T) {
	proc := &bpmnProcess{
		StartEvents: []bpmnEvent{{ID: "S1"}},
		EndEvents:   []bpmnEvent{{ID: "E1"}, {ID: "E2"}, {ID: "E3"}},
	}
	errs := validateCounts(proc)
	if !hasCodeIn(errs, domain.BPMNErrMultipleEndEvents) {
		t.Errorf("expected MULTIPLE_END_EVENTS; got %v", errCodesOf(errs))
	}
}

func TestValidateCounts_LaneLimitExceeded(t *testing.T) {
	lanes := make([]bpmnLane, maxLanes+1)
	for i := range lanes {
		lanes[i] = bpmnLane{ID: "lane", Name: "lane"}
	}
	proc := &bpmnProcess{
		StartEvents: []bpmnEvent{{ID: "S1"}},
		EndEvents:   []bpmnEvent{{ID: "E1"}},
		LaneSet:     bpmnLaneSet{Lanes: lanes},
	}
	errs := validateCounts(proc)
	if !hasCodeIn(errs, domain.BPMNErrLaneLimitExceeded) {
		t.Errorf("expected LANE_LIMIT_EXCEEDED; got %v", errCodesOf(errs))
	}
}

func TestValidateCounts_TaskLimitExceeded(t *testing.T) {
	tasks := make([]bpmnUserTask, maxUserTasks+1)
	proc := &bpmnProcess{
		StartEvents: []bpmnEvent{{ID: "S1"}},
		EndEvents:   []bpmnEvent{{ID: "E1"}},
		UserTasks:   tasks,
	}
	errs := validateCounts(proc)
	if !hasCodeIn(errs, domain.BPMNErrTaskLimitExceeded) {
		t.Errorf("expected TASK_LIMIT_EXCEEDED; got %v", errCodesOf(errs))
	}
}

func TestValidateSeqFlowRefs_InvalidSourceRef(t *testing.T) {
	proc := &bpmnProcess{
		SequenceFlows: []bpmnSequenceFlow{
			{ID: "F1", SourceRef: "ghost-node", TargetRef: "S1"},
		},
	}
	g := &graph{
		nodeIDs: map[string]struct{}{"S1": {}},
	}
	errs := validateSeqFlowRefs(proc, g)
	if !hasCodeIn(errs, domain.BPMNErrInvalidSequenceFlowRef) {
		t.Errorf("expected INVALID_SEQUENCE_FLOW_REF for invalid sourceRef; got %v", errCodesOf(errs))
	}
}

func TestValidateSeqFlowRefs_InvalidTargetRef(t *testing.T) {
	proc := &bpmnProcess{
		SequenceFlows: []bpmnSequenceFlow{
			{ID: "F1", SourceRef: "S1", TargetRef: "ghost-node"},
		},
	}
	g := &graph{
		nodeIDs: map[string]struct{}{"S1": {}},
	}
	errs := validateSeqFlowRefs(proc, g)
	if !hasCodeIn(errs, domain.BPMNErrInvalidSequenceFlowRef) {
		t.Errorf("expected INVALID_SEQUENCE_FLOW_REF for invalid targetRef; got %v", errCodesOf(errs))
	}
}

func TestValidateTaskProps_RoleTooLong(t *testing.T) {
	task := bpmnUserTask{
		ID: "T1",
		ExtensionElements: bpmnExtensionElements{ZeebeProps: bpmnZeebeProperties{Items: []zeebeProperty{
			{Name: "dept_id", Value: "design"},
			{Name: "stage_type", Value: "prep"},
			{Name: "role", Value: strings.Repeat("x", maxRoleLen+1)},
			{Name: "default_user_ids", Value: aliceUUID},
		}}},
	}
	errs := validateTaskProps(task, map[string]struct{}{"design": {}})
	if !hasCodeIn(errs, domain.BPMNErrRoleEmpty) {
		t.Errorf("expected ROLE_EMPTY for over-long role; got %v", errCodesOf(errs))
	}
}

func TestValidateTaskProps_InvalidRequiresComment(t *testing.T) {
	task := bpmnUserTask{
		ID: "T1",
		ExtensionElements: bpmnExtensionElements{ZeebeProps: bpmnZeebeProperties{Items: []zeebeProperty{
			{Name: "dept_id", Value: "design"},
			{Name: "stage_type", Value: "prep"},
			{Name: "role", Value: "preparer"},
			{Name: "default_user_ids", Value: aliceUUID},
			{Name: "requires_comment", Value: "maybe"},
		}}},
	}
	errs := validateTaskProps(task, map[string]struct{}{"design": {}})
	if !hasCodeIn(errs, domain.BPMNErrInvalidZeebeProperty) {
		t.Errorf("expected INVALID_ZEEBE_PROPERTY for bad requires_comment; got %v", errCodesOf(errs))
	}
}

func TestValidateTaskProps_MissingRole(t *testing.T) {
	task := bpmnUserTask{
		ID: "T1",
		ExtensionElements: bpmnExtensionElements{ZeebeProps: bpmnZeebeProperties{Items: []zeebeProperty{
			{Name: "dept_id", Value: "design"},
			{Name: "stage_type", Value: "prep"},
			{Name: "default_user_ids", Value: aliceUUID},
		}}},
	}
	errs := validateTaskProps(task, map[string]struct{}{"design": {}})
	if !hasCodeIn(errs, domain.BPMNErrMissingZeebeProperty) {
		t.Errorf("expected MISSING_ZEEBE_PROPERTY for absent role; got %v", errCodesOf(errs))
	}
}

func TestValidateGatewayMatching_ExclusiveNoJoin(t *testing.T) {
	// An exclusive split (2 outgoing) with branches that end without converging.
	proc := &bpmnProcess{
		ExclusiveGateways: []bpmnGateway{{ID: "GW_split"}},
	}
	g := &graph{
		outgoing: map[string][]string{
			"GW_split": {"TaskA", "TaskB"},
			"TaskA":    {"End1"},
			"TaskB":    {"End2"},
		},
		incoming: map[string][]string{
			"TaskA": {"GW_split"},
			"TaskB": {"GW_split"},
			"End1":  {"TaskA"},
			"End2":  {"TaskB"},
		},
		nodeType: map[string]FlowNodeType{
			"GW_split": NodeTypeExclusiveGateway,
			"TaskA":    NodeTypeUserTask,
			"TaskB":    NodeTypeUserTask,
			"End1":     NodeTypeEndEvent,
			"End2":     NodeTypeEndEvent,
		},
	}
	errs := validateGatewayMatching(proc, g)
	if !hasCodeIn(errs, domain.BPMNErrUnmatchedGateway) {
		t.Errorf("expected UNMATCHED_GATEWAY for exclusive split; got %v", errCodesOf(errs))
	}
}

func TestValidateGatewayMatching_ParallelNoJoin(t *testing.T) {
	proc := &bpmnProcess{
		ParallelGateways: []bpmnGateway{{ID: "GW_split"}},
	}
	g := &graph{
		outgoing: map[string][]string{
			"GW_split": {"TaskA", "TaskB"},
			"TaskA":    {"End1"},
			"TaskB":    {"End2"},
		},
		incoming: map[string][]string{
			"TaskA": {"GW_split"},
			"TaskB": {"GW_split"},
			"End1":  {"TaskA"},
			"End2":  {"TaskB"},
		},
		nodeType: map[string]FlowNodeType{
			"GW_split": NodeTypeParallelGateway,
		},
	}
	errs := validateGatewayMatching(proc, g)
	if !hasCodeIn(errs, domain.BPMNErrUnmatchedGateway) {
		t.Errorf("expected UNMATCHED_GATEWAY for parallel split; got %v", errCodesOf(errs))
	}
}

func TestValidateConditionExprs_TooLong(t *testing.T) {
	longExpr := strings.Repeat("x", maxCondExpr+1)
	proc := &bpmnProcess{
		SequenceFlows: []bpmnSequenceFlow{
			{
				ID: "F1", SourceRef: "GW_split", TargetRef: "TaskA",
				ExtensionElements: bpmnExtensionElements{ZeebeProps: bpmnZeebeProperties{Items: []zeebeProperty{
					{Name: "condition_expression", Value: longExpr},
				}}},
			},
		},
	}
	g := &graph{
		outgoing: map[string][]string{"GW_split": {"TaskA", "TaskB"}},
	}
	errs := validateConditionExprs(proc, g)
	if !hasCodeIn(errs, domain.BPMNErrInvalidZeebeProperty) {
		t.Errorf("expected INVALID_ZEEBE_PROPERTY for over-long condition_expression; got %v", errCodesOf(errs))
	}
}

// ---------------------------------------------------------------------------
// checkDangling
// ---------------------------------------------------------------------------

func TestCheckDangling_NoIncoming(t *testing.T) {
	g := &graph{
		incoming: map[string][]string{},
		outgoing: map[string][]string{"T1": {"E1"}},
	}
	errs := checkDangling("T1", g)
	if !hasCodeIn(errs, domain.BPMNErrDanglingNode) {
		t.Errorf("expected DANGLING_NODE for no incoming; got %v", errCodesOf(errs))
	}
}

func TestCheckDangling_NoOutgoing(t *testing.T) {
	g := &graph{
		incoming: map[string][]string{"T1": {"S1"}},
		outgoing: map[string][]string{},
	}
	errs := checkDangling("T1", g)
	if !hasCodeIn(errs, domain.BPMNErrDanglingNode) {
		t.Errorf("expected DANGLING_NODE for no outgoing; got %v", errCodesOf(errs))
	}
}

func TestCheckDangling_Connected(t *testing.T) {
	g := &graph{
		incoming: map[string][]string{"T1": {"S1"}},
		outgoing: map[string][]string{"T1": {"E1"}},
	}
	if errs := checkDangling("T1", g); len(errs) != 0 {
		t.Errorf("expected no errors for connected node; got %v", errs)
	}
}

// ---------------------------------------------------------------------------
// validateDanglingNodes — event-specific branches
// ---------------------------------------------------------------------------

func TestValidateDanglingNodes_StartNoOutgoing(t *testing.T) {
	proc := &bpmnProcess{
		StartEvents: []bpmnEvent{{ID: "S1"}},
	}
	g := &graph{
		outgoing: map[string][]string{},
		incoming: map[string][]string{},
	}
	errs := validateDanglingNodes(proc, g)
	if !hasCodeIn(errs, domain.BPMNErrDanglingNode) {
		t.Errorf("expected DANGLING_NODE for start event with no outgoing; got %v", errCodesOf(errs))
	}
}

func TestValidateDanglingNodes_EndNoIncoming(t *testing.T) {
	proc := &bpmnProcess{
		EndEvents: []bpmnEvent{{ID: "E1"}},
	}
	g := &graph{
		outgoing: map[string][]string{},
		incoming: map[string][]string{},
	}
	errs := validateDanglingNodes(proc, g)
	if !hasCodeIn(errs, domain.BPMNErrDanglingNode) {
		t.Errorf("expected DANGLING_NODE for end event with no incoming; got %v", errCodesOf(errs))
	}
}

// ---------------------------------------------------------------------------
// validateReachability
// ---------------------------------------------------------------------------

func TestValidateReachability_NoStartEvent(t *testing.T) {
	proc := &bpmnProcess{}
	g := &graph{nodeIDs: map[string]struct{}{"T1": {}}}
	// Guard: no start events → returns nil (cannot do reachability without start).
	errs := validateReachability(proc, g)
	if len(errs) != 0 {
		t.Errorf("expected empty errs when no start event; got %v", errs)
	}
}

func TestValidateReachability_UnreachableNode(t *testing.T) {
	proc := &bpmnProcess{StartEvents: []bpmnEvent{{ID: "S1"}}}
	g := &graph{
		nodeIDs: map[string]struct{}{"S1": {}, "orphan": {}},
		outgoing: map[string][]string{
			"S1": {},
		},
	}
	errs := validateReachability(proc, g)
	if !hasCodeIn(errs, domain.BPMNErrUnreachableNode) {
		t.Errorf("expected UNREACHABLE_NODE; got %v", errCodesOf(errs))
	}
}

func TestValidateReachability_AllReachable(t *testing.T) {
	proc := &bpmnProcess{StartEvents: []bpmnEvent{{ID: "S1"}}}
	g := &graph{
		nodeIDs:  map[string]struct{}{"S1": {}, "T1": {}, "E1": {}},
		outgoing: map[string][]string{"S1": {"T1"}, "T1": {"E1"}},
	}
	if errs := validateReachability(proc, g); len(errs) != 0 {
		t.Errorf("expected no errors when all nodes reachable; got %v", errs)
	}
}

// ---------------------------------------------------------------------------
// validateCycles
// ---------------------------------------------------------------------------

func TestValidateCycles_NoCycle(t *testing.T) {
	g := &graph{
		nodeIDs:  map[string]struct{}{"S1": {}, "T1": {}, "E1": {}},
		outgoing: map[string][]string{"S1": {"T1"}, "T1": {"E1"}},
		incoming: map[string][]string{"T1": {"S1"}, "E1": {"T1"}},
	}
	if errs := validateCycles(g); len(errs) != 0 {
		t.Errorf("expected no errors for acyclic graph; got %v", errs)
	}
}

func TestValidateCycles_WithCycle(t *testing.T) {
	g := &graph{
		nodeIDs:  map[string]struct{}{"T1": {}, "T2": {}},
		outgoing: map[string][]string{"T1": {"T2"}, "T2": {"T1"}},
		incoming: map[string][]string{"T1": {"T2"}, "T2": {"T1"}},
	}
	errs := validateCycles(g)
	if !hasCodeIn(errs, domain.BPMNErrCycleDetected) {
		t.Errorf("expected CYCLE_DETECTED; got %v", errCodesOf(errs))
	}
}

// ---------------------------------------------------------------------------
// buildLaneNameSet
// ---------------------------------------------------------------------------

func TestBuildLaneNameSet(t *testing.T) {
	proc := &bpmnProcess{
		LaneSet: bpmnLaneSet{Lanes: []bpmnLane{
			{Name: "Design"},
			{Name: "  QA  "},
			{Name: "HSE"},
		}},
	}
	m := buildLaneNameSet(proc)
	for _, want := range []string{"design", "qa", "hse"} {
		if _, ok := m[want]; !ok {
			t.Errorf("expected %q in lane name set", want)
		}
	}
	if _, ok := m["Design"]; ok {
		t.Error("lane name set should store normalized (lowercase) names")
	}
}

// ---------------------------------------------------------------------------
// validateTaskDeptID / validateTaskStageType / validateTaskRole / validateTaskDefaultUserIDs
// ---------------------------------------------------------------------------

func TestValidateTaskDeptID_Missing(t *testing.T) {
	task := bpmnUserTask{ID: "T1"}
	errs := validateTaskDeptID(task, map[string]string{}, map[string]struct{}{"design": {}})
	if !hasCodeIn(errs, domain.BPMNErrMissingZeebeProperty) {
		t.Errorf("expected MISSING_ZEEBE_PROPERTY; got %v", errCodesOf(errs))
	}
}

func TestValidateTaskDeptID_Invalid(t *testing.T) {
	task := bpmnUserTask{ID: "T1"}
	props := map[string]string{"dept_id": "finance"}
	errs := validateTaskDeptID(task, props, map[string]struct{}{"design": {}})
	if !hasCodeIn(errs, domain.BPMNErrInvalidDeptID) {
		t.Errorf("expected INVALID_DEPT_ID; got %v", errCodesOf(errs))
	}
}

func TestValidateTaskStageType_Missing(t *testing.T) {
	task := bpmnUserTask{ID: "T1"}
	errs := validateTaskStageType(task, map[string]string{})
	if !hasCodeIn(errs, domain.BPMNErrMissingZeebeProperty) {
		t.Errorf("expected MISSING_ZEEBE_PROPERTY; got %v", errCodesOf(errs))
	}
}

func TestValidateTaskStageType_Invalid(t *testing.T) {
	task := bpmnUserTask{ID: "T1"}
	errs := validateTaskStageType(task, map[string]string{"stage_type": "audit"})
	if !hasCodeIn(errs, domain.BPMNErrInvalidStageType) {
		t.Errorf("expected INVALID_STAGE_TYPE; got %v", errCodesOf(errs))
	}
}

func TestValidateTaskDefaultUserIDs_AssigneeLimitExceeded(t *testing.T) {
	ids := make([]string, maxDefaultAssignees+1)
	for i := range ids {
		ids[i] = aliceUUID
	}
	props := map[string]string{"default_user_ids": strings.Join(ids, ",")}
	task := bpmnUserTask{ID: "T1"}
	errs := validateTaskDefaultUserIDs(task, props)
	if !hasCodeIn(errs, domain.BPMNErrTaskAssigneeLimitExceeded) {
		t.Errorf("expected TASK_ASSIGNEE_LIMIT_EXCEEDED; got %v", errCodesOf(errs))
	}
}

func TestValidateTaskDefaultUserIDs_AssigneeLimitAtMax(t *testing.T) {
	ids := make([]string, maxDefaultAssignees)
	for i := range ids {
		ids[i] = aliceUUID
	}
	props := map[string]string{"default_user_ids": strings.Join(ids, ",")}
	task := bpmnUserTask{ID: "T1"}
	errs := validateTaskDefaultUserIDs(task, props)
	if hasCodeIn(errs, domain.BPMNErrTaskAssigneeLimitExceeded) {
		t.Error("should not emit TASK_ASSIGNEE_LIMIT_EXCEEDED at exactly max assignees")
	}
}

// ---------------------------------------------------------------------------
// validateTaskOptionalProps (assignee_mode)
// ---------------------------------------------------------------------------

func TestValidateTaskOptionalProps_InvalidAssigneeMode(t *testing.T) {
	task := bpmnUserTask{ID: "T1"}
	errs := validateTaskOptionalProps(task, map[string]string{"assignee_mode": "solo"})
	if !hasCodeIn(errs, domain.BPMNErrInvalidZeebeProperty) {
		t.Errorf("expected INVALID_ZEEBE_PROPERTY for bad assignee_mode; got %v", errCodesOf(errs))
	}
}

func TestValidateTaskOptionalProps_ValidAssigneeMode(t *testing.T) {
	task := bpmnUserTask{ID: "T1"}
	for _, mode := range []string{"any", "all"} {
		errs := validateTaskOptionalProps(task, map[string]string{"assignee_mode": mode})
		if hasCodeIn(errs, domain.BPMNErrInvalidZeebeProperty) {
			t.Errorf("mode %q should be valid; got %v", mode, errs)
		}
	}
}

// ---------------------------------------------------------------------------
// validateGatewayMatching — passthrough (single outgoing, not a split)
// ---------------------------------------------------------------------------

func TestValidateGatewayMatching_PassthroughGateway(t *testing.T) {
	proc := &bpmnProcess{
		ParallelGateways: []bpmnGateway{{ID: "GW_pass"}},
	}
	g := &graph{
		outgoing: map[string][]string{"GW_pass": {"T1"}},
	}
	errs := validateGatewayMatching(proc, g)
	if len(errs) != 0 {
		t.Errorf("passthrough gateway (1 outgoing) should produce no errors; got %v", errs)
	}
}

// ---------------------------------------------------------------------------
// validateConditionExprs — skip branches
// ---------------------------------------------------------------------------

func TestValidateConditionExprs_SingleOutflow_Skipped(t *testing.T) {
	proc := &bpmnProcess{
		SequenceFlows: []bpmnSequenceFlow{
			{
				ID: "F1", SourceRef: "T1", TargetRef: "E1",
				ExtensionElements: bpmnExtensionElements{ZeebeProps: bpmnZeebeProperties{Items: []zeebeProperty{
					{Name: "condition_expression", Value: strings.Repeat("x", maxCondExpr+1)},
				}}},
			},
		},
	}
	// Source T1 has only 1 outgoing → validation should be skipped.
	g := &graph{outgoing: map[string][]string{"T1": {"E1"}}}
	if errs := validateConditionExprs(proc, g); len(errs) != 0 {
		t.Errorf("expected no error for single-outflow source; got %v", errs)
	}
}

func TestValidateConditionExprs_NoConditionProp(t *testing.T) {
	proc := &bpmnProcess{
		SequenceFlows: []bpmnSequenceFlow{
			{ID: "F1", SourceRef: "GW1", TargetRef: "T1"},
		},
	}
	g := &graph{outgoing: map[string][]string{"GW1": {"T1", "T2"}}}
	if errs := validateConditionExprs(proc, g); len(errs) != 0 {
		t.Errorf("expected no error when condition_expression prop is absent; got %v", errs)
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
