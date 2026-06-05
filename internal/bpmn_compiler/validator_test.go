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
