package bpmn_compiler

import (
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/validator"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func w2aDeptLane(laneID string, refs []string, deptID string) bpmncore.BPMNLane {
	lane := bpmncore.BPMNLane{ID: laneID, Name: laneID, FlowNodeRefs: refs}
	if deptID != "" {
		lane.ExtensionElements.ZeebeProps = bpmncore.BPMNZeebeProperties{Items: []bpmncore.ZeebeProperty{{Name: "dept_id", Value: deptID}}}
	}
	return lane
}

func w2aIgnoredLane(laneID string, refs []string) bpmncore.BPMNLane {
	lane := bpmncore.BPMNLane{ID: laneID, Name: laneID, FlowNodeRefs: refs}
	lane.ExtensionElements.ZeebeProps = bpmncore.BPMNZeebeProperties{Items: []bpmncore.ZeebeProperty{{Name: "ignore", Value: "true"}}}
	return lane
}

func w2aCodesFor(errs []domain.BPMNValidationError, nodeID string) []domain.BPMNErrorCode {
	var codes []domain.BPMNErrorCode
	for _, e := range errs {
		if e.NodeID == nodeID {
			codes = append(codes, e.Code)
		}
	}
	return codes
}

func w2aNodesWithCode(errs []domain.BPMNValidationError, code domain.BPMNErrorCode) []string {
	var ids []string
	for _, e := range errs {
		if e.Code == code {
			ids = append(ids, e.NodeID)
		}
	}
	sort.Strings(ids)
	return ids
}

func TestValidateDeptID_ModuleProcessAndUUIDVersions(t *testing.T) {
	t.Parallel()

	const processID = "Process_W2A"
	laneWith := func(dept string) []bpmncore.BPMNLane {
		return []bpmncore.BPMNLane{w2aDeptLane("L1", []string{"T1"}, dept)}
	}
	v4 := uuid.NewString()
	v1, err := uuid.NewUUID()
	if err != nil {
		t.Fatalf("uuid.NewUUID: %v", err)
	}
	cases := []struct {
		name     string
		lanes    []bpmncore.BPMNLane
		taskID   string
		defs     *bpmncore.BPMNDefinitions
		wantCode domain.BPMNErrorCode
	}{
		{
			name:     "missing dept_id in a regular process",
			lanes:    laneWith(""),
			taskID:   "T1",
			defs:     &bpmncore.BPMNDefinitions{ModuleProcesses: map[string]bool{"Other_Process": true}},
			wantCode: domain.BPMNErrMissingDeptID,
		},
		{
			name:     "missing dept_id with definitions that list no module process",
			lanes:    laneWith(""),
			taskID:   "T1",
			defs:     &bpmncore.BPMNDefinitions{},
			wantCode: domain.BPMNErrMissingDeptID,
		},
		{
			name:   "missing dept_id in a module process is allowed",
			lanes:  laneWith(""),
			taskID: "T1",
			defs:   &bpmncore.BPMNDefinitions{ModuleProcesses: map[string]bool{processID: true}},
		},
		{
			name:     "module process marked false is a regular process",
			lanes:    laneWith(""),
			taskID:   "T1",
			defs:     &bpmncore.BPMNDefinitions{ModuleProcesses: map[string]bool{processID: false}},
			wantCode: domain.BPMNErrMissingDeptID,
		},
		{
			name:     "a module process still refuses a dept_id that is not a UUID",
			lanes:    laneWith("not-a-uuid"),
			taskID:   "T1",
			defs:     &bpmncore.BPMNDefinitions{ModuleProcesses: map[string]bool{processID: true}},
			wantCode: domain.BPMNErrInvalidDeptID,
		},
		{
			name:   "a version 4 UUID is accepted",
			lanes:  laneWith(v4),
			taskID: "T1",
		},
		{
			name:   "a version 1 UUID is accepted",
			lanes:  laneWith(v1.String()),
			taskID: "T1",
		},
		{
			name:   "a version 7 UUID is accepted",
			lanes:  laneWith("018e1f2a-0000-7000-8000-000000000099"),
			taskID: "T1",
		},
		{
			name:     "a UUID with a trailing character is refused",
			lanes:    laneWith(v4 + "x"),
			taskID:   "T1",
			wantCode: domain.BPMNErrInvalidDeptID,
		},
		{
			name:   "a task that is in no lane is not checked",
			lanes:  laneWith(""),
			taskID: "T_outside",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			proc := &bpmncore.BPMNProcess{ID: processID, LaneSet: bpmncore.BPMNLaneSet{Lanes: tc.lanes}}

			errs := validator.ValidateDeptID(tc.taskID, proc, tc.defs)

			if tc.wantCode == "" {
				if len(errs) != 0 {
					t.Fatalf("want no error, got %v", errCodesOf(errs))
				}
				return
			}
			if len(errs) != 1 {
				t.Fatalf("want exactly one %s error, got %v", tc.wantCode, errCodesOf(errs))
			}
			if errs[0].Code != tc.wantCode || errs[0].NodeID != tc.taskID || errs[0].Severity != domain.SeverityError {
				t.Errorf("got %+v, want an error-severity %s on %s", errs[0], tc.wantCode, tc.taskID)
			}
			if !strings.Contains(errs[0].Message, `"L1"`) {
				t.Errorf("message %q does not name the lane", errs[0].Message)
			}
		})
	}
}

func w2aChainGraph(n int) *bpmncore.Graph {
	g := &bpmncore.Graph{
		Outgoing: map[string][]string{},
		Incoming: map[string][]string{},
		NodeIDs:  map[string]struct{}{},
		NodeType: map[string]bpmncore.FlowNodeType{},
	}
	prev := "N0"
	g.NodeIDs[prev] = struct{}{}
	g.NodeType[prev] = bpmncore.NodeTypeReceiveTask
	for i := 1; i < n; i++ {
		cur := "N" + strconv.Itoa(i)
		g.Outgoing[prev] = []string{cur}
		g.Incoming[cur] = []string{prev}
		g.NodeIDs[cur] = struct{}{}
		g.NodeType[cur] = bpmncore.NodeTypeExclusiveGateway
		prev = cur
	}
	return g
}

func w2aProcessWithLaneLimitAndBadLaneName() *bpmncore.BPMNProcess {
	lanes := make([]bpmncore.BPMNLane, validator.MaxLanes+1)
	for i := range lanes {
		name := "lane" + strconv.Itoa(i)
		if i == 0 {
			name = "has/slash"
		}
		lanes[i] = bpmncore.BPMNLane{ID: "L" + strconv.Itoa(i), Name: name}
	}
	return &bpmncore.BPMNProcess{LaneSet: bpmncore.BPMNLaneSet{Lanes: lanes}}
}

func TestValidateWithImplicitStart_PathDepthBoundary_SkipsLaterValidatorsOnlyWhenOver(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		nodes         int
		wantDepthErr  bool
		wantLaterRuns bool
	}{
		{name: "exactly the limit passes and later validators run", nodes: validator.MaxPathDepth, wantDepthErr: false, wantLaterRuns: true},
		{name: "one node over the limit stops after the count errors", nodes: validator.MaxPathDepth + 1, wantDepthErr: true, wantLaterRuns: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			proc := w2aProcessWithLaneLimitAndBadLaneName()
			g := w2aChainGraph(tc.nodes)

			errs := validator.ValidateWithImplicitStart(proc, g, "N0", map[string]bpmncore.StageTypeHandler{}, map[bpmncore.FlowNodeType]bpmncore.ElementHandler{}, &bpmncore.BPMNDefinitions{})

			if got := hasCodeIn(errs, domain.BPMNErrMaxDepthExceeded); got != tc.wantDepthErr {
				t.Fatalf("MAX_DEPTH_EXCEEDED present = %v, want %v (codes %v)", got, tc.wantDepthErr, errCodesOf(errs))
			}
			if !hasCodeIn(errs, domain.BPMNErrLaneLimitExceeded) {
				t.Errorf("the count error LANE_LIMIT_EXCEEDED is always reported; got %v", errCodesOf(errs))
			}
			if got := hasCodeIn(errs, domain.BPMNErrInvalidLaneName); got != tc.wantLaterRuns {
				t.Errorf("a later validator's INVALID_LANE_NAME present = %v, want %v (codes %v)", got, tc.wantLaterRuns, errCodesOf(errs))
			}
			if tc.wantDepthErr {
				codes := errCodesOf(errs)
				want := []domain.BPMNErrorCode{domain.BPMNErrLaneLimitExceeded, domain.BPMNErrMaxDepthExceeded}
				if len(codes) != len(want) || codes[0] != want[0] || codes[1] != want[1] {
					t.Errorf("codes = %v, want exactly the count error then the depth error %v", codes, want)
				}
				if errs[1].NodeID != "N0" {
					t.Errorf("depth error is on %q, want the implicit start N0", errs[1].NodeID)
				}
			}
		})
	}
}

type w2aElementStub struct {
	kind bpmncore.FlowNodeType
}

func (s w2aElementStub) NodeType() bpmncore.FlowNodeType { return s.kind }

func (s w2aElementStub) Validate(nodeID string, _ *bpmncore.BPMNProcess, _ *bpmncore.Graph, _ *bpmncore.BPMNDefinitions, _ map[string]bpmncore.StageTypeHandler, _ map[bpmncore.FlowNodeType]bpmncore.ElementHandler) []domain.BPMNValidationError {
	return []domain.BPMNValidationError{{Code: domain.BPMNErrMissingTaskDefinition, NodeID: nodeID, Message: "w2a stub handler ran", Severity: domain.SeverityError}}
}

func (s w2aElementStub) Compile(string, *bpmncore.CompileState) error { return nil }

func w2aPerElementProcess() *bpmncore.BPMNProcess {
	emptyGroups := bpmncore.BPMNExtensionElements{AssignmentDefinition: &bpmncore.ZeebeAssignmentDefinition{CandidateGroups: ""}}
	validGroups := bpmncore.BPMNExtensionElements{AssignmentDefinition: &bpmncore.ZeebeAssignmentDefinition{CandidateGroups: "preparator", CandidateUsers: aliceUUID}}
	dept := uuid.NewString()
	return &bpmncore.BPMNProcess{
		ID: "Process_W2A",
		LaneSet: bpmncore.BPMNLaneSet{Lanes: []bpmncore.BPMNLane{
			w2aDeptLane("Ops", []string{"T1", "S2", "S3", "S4", "R1", "R2", "R4", "SP1", "CA1"}, dept),
			w2aIgnoredLane("Skip", []string{"T2", "S1", "R3"}),
		}},
		UserTasks:      []bpmncore.BPMNUserTask{{ID: "T1"}, {ID: "T2"}},
		SubProcesses:   []bpmncore.BPMNSubProcess{{ID: "SP1"}},
		CallActivities: []bpmncore.BPMNCallActivity{{ID: "CA1"}},
		SendTasks: []bpmncore.BPMNSendTask{
			{ID: "S1", ExtensionElements: emptyGroups},
			{ID: "S2"},
			{ID: "S3", ExtensionElements: emptyGroups},
			{ID: "S4", ExtensionElements: validGroups},
		},
		ReceiveTasks: []bpmncore.BPMNReceiveTask{
			{ID: "R1"},
			{ID: "R2", ExtensionElements: emptyGroups},
			{ID: "R3", ExtensionElements: emptyGroups},
			{ID: "R4", ExtensionElements: validGroups},
		},
	}
}

func TestValidate_PerElementRules_SkippedTasksAndMissingHandlers(t *testing.T) {
	t.Parallel()

	perElementCodes := []domain.BPMNErrorCode{
		domain.BPMNErrCandidateGroupsEmpty,
		domain.BPMNErrMissingAssignmentDefinition,
		domain.BPMNErrMissingDeptID,
		domain.BPMNErrTaskNotInLane,
	}
	cases := []struct {
		name         string
		elements     map[bpmncore.FlowNodeType]bpmncore.ElementHandler
		wantHandled  []string
		wantEmptyGrp []string
	}{
		{
			name: "handlers validate every task outside an ignored lane",
			elements: map[bpmncore.FlowNodeType]bpmncore.ElementHandler{
				bpmncore.NodeTypeUserTask:     w2aElementStub{kind: bpmncore.NodeTypeUserTask},
				bpmncore.NodeTypeSubProcess:   w2aElementStub{kind: bpmncore.NodeTypeSubProcess},
				bpmncore.NodeTypeCallActivity: w2aElementStub{kind: bpmncore.NodeTypeCallActivity},
			},
			wantHandled:  []string{"CA1", "SP1", "T1"},
			wantEmptyGrp: []string{"R2", "S3"},
		},
		{
			name:         "missing handlers are skipped silently and the send and receive rules still run",
			elements:     map[bpmncore.FlowNodeType]bpmncore.ElementHandler{},
			wantHandled:  nil,
			wantEmptyGrp: []string{"R2", "S3"},
		},
		{
			name:         "a nil handler map behaves like an empty one",
			elements:     nil,
			wantHandled:  nil,
			wantEmptyGrp: []string{"R2", "S3"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			proc := w2aPerElementProcess()
			g := bpmncore.BuildGraph(proc)

			errs := validator.ValidateWithImplicitStart(proc, g, "", map[string]bpmncore.StageTypeHandler{}, tc.elements, &bpmncore.BPMNDefinitions{})

			handled := w2aNodesWithCode(errs, domain.BPMNErrMissingTaskDefinition)
			if strings.Join(handled, ",") != strings.Join(tc.wantHandled, ",") {
				t.Errorf("nodes the element handlers ran for = %v, want %v", handled, tc.wantHandled)
			}
			emptyGroups := w2aNodesWithCode(errs, domain.BPMNErrCandidateGroupsEmpty)
			if strings.Join(emptyGroups, ",") != strings.Join(tc.wantEmptyGrp, ",") {
				t.Errorf("nodes with an assignment that names no group = %v, want %v", emptyGroups, tc.wantEmptyGrp)
			}
			for _, id := range []string{"S1", "R3", "T2"} {
				for _, code := range w2aCodesFor(errs, id) {
					for _, per := range perElementCodes {
						if code == per {
							t.Errorf("skipped task %s got %s, a per-element validator must not run for it", id, code)
						}
					}
				}
			}
			for _, id := range []string{"S2", "S4", "R1", "R4"} {
				for _, code := range w2aCodesFor(errs, id) {
					for _, per := range perElementCodes {
						if code == per {
							t.Errorf("task %s is valid or has no assignment, but got %s", id, code)
						}
					}
				}
			}
		})
	}
}

func TestValidateTaskExtensions_MissingAssignmentDefinition_IsTheOnlyErrorAndSkippedTasksAreChecked(t *testing.T) {
	t.Parallel()

	taskDef := &bpmncore.ZeebeTaskDefinition{Type: "prep"}
	proc := &bpmncore.BPMNProcess{
		LaneSet: bpmncore.BPMNLaneSet{Lanes: []bpmncore.BPMNLane{
			w2aDeptLane("Ops", []string{"T1"}, ""),
			w2aIgnoredLane("Skip", []string{"T2", "T3"}),
		}},
		UserTasks: []bpmncore.BPMNUserTask{
			{ID: "T1", ExtensionElements: bpmncore.BPMNExtensionElements{TaskDefinition: taskDef}},
			{ID: "T2", ExtensionElements: bpmncore.BPMNExtensionElements{TaskDefinition: taskDef}},
			{ID: "T3"},
		},
	}

	errs := validator.ValidateTaskExtensions(proc, defaultStageTypes())

	want := map[string][]domain.BPMNErrorCode{
		"T1": {domain.BPMNErrMissingAssignmentDefinition},
		"T2": {domain.BPMNErrMissingAssignmentDefinition},
		"T3": {domain.BPMNErrMissingTaskDefinition, domain.BPMNErrMissingAssignmentDefinition},
	}
	for id, wantCodes := range want {
		got := w2aCodesFor(errs, id)
		if len(got) != len(wantCodes) {
			t.Errorf("%s: codes = %v, want exactly %v", id, got, wantCodes)
			continue
		}
		for i := range got {
			if got[i] != wantCodes[i] {
				t.Errorf("%s: codes = %v, want %v", id, got, wantCodes)
			}
		}
	}
	if len(errs) != 4 {
		t.Errorf("total errors = %d (%v), want 4: no role-level, default-assignee or lane error for a task without an assignmentDefinition", len(errs), errCodesOf(errs))
	}
	for _, e := range errs {
		if e.Severity != domain.SeverityError {
			t.Errorf("%s on %s has severity %q, want error", e.Code, e.NodeID, e.Severity)
		}
	}
}
