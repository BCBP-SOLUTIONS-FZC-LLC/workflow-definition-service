package bpmn_compiler

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/element"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

// ─── filterCode ───────────────────────────────────────────────────────────

func TestFilterCode(t *testing.T) {
	errs := []domain.BPMNValidationError{
		{Code: domain.BPMNErrNoEndEvent, NodeID: "A"},
		{Code: domain.BPMNErrTaskNotInLane, NodeID: "B"},
		{Code: domain.BPMNErrNoEndEvent, NodeID: "C"},
	}
	out := filterCode(errs)
	if len(out) != 1 || out[0].NodeID != "B" {
		t.Errorf("filterCode() = %+v, want only NodeID=B", out)
	}
}

func TestFilterCode_EmptyAndNil(t *testing.T) {
	if out := filterCode(nil); len(out) != 0 {
		t.Errorf("filterCode(nil) = %v, want empty", out)
	}
	if out := filterCode([]domain.BPMNValidationError{}); len(out) != 0 {
		t.Errorf("filterCode([]) = %v, want empty", out)
	}
}

// ─── hasTerminalBridges ───────────────────────────────────────────────────

func TestHasTerminalBridges(t *testing.T) {
	if hasTerminalBridges(nil) {
		t.Error("nil bridges: expected false")
	}
	if hasTerminalBridges([]bpmncore.MessageBridge{}) {
		t.Error("empty bridges: expected false")
	}
	if hasTerminalBridges([]bpmncore.MessageBridge{{ToTaskID: "x"}}) {
		t.Error("all bridges resolved: expected false")
	}
	if !hasTerminalBridges([]bpmncore.MessageBridge{{ToTaskID: "x"}, {ToTaskID: ""}}) {
		t.Error("one terminal bridge present: expected true")
	}
}

// ─── isIgnoredProcess ─────────────────────────────────────────────────────

func TestIsIgnoredProcess(t *testing.T) {
	tests := []struct {
		name string
		proc *bpmncore.BPMNProcess
		want bool
	}{
		{"empty extension elements", &bpmncore.BPMNProcess{}, false},
		{"wrong value", propProc("ignore", "false"), false},
		{"wrong name", propProc("other", "true"), false},
		{"matching prop", propProc("ignore", "true"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isIgnoredProcess(tt.proc); got != tt.want {
				t.Errorf("isIgnoredProcess() = %v, want %v", got, tt.want)
			}
		})
	}
}

func propProc(name, value string) *bpmncore.BPMNProcess {
	return &bpmncore.BPMNProcess{
		ExtensionElements: bpmncore.BPMNExtensionElements{
			ZeebeProps: bpmncore.BPMNZeebeProperties{Items: []bpmncore.ZeebeProperty{{Name: name, Value: value}}},
		},
	}
}

// ─── hasBlockingErrors (additional branch) ───────────────────────────────

func TestHasBlockingErrors_AllWarnings(t *testing.T) {
	errs := []domain.BPMNValidationError{{Severity: domain.SeverityWarning}}
	if hasBlockingErrors(errs) {
		t.Error("all-warning errs should not be blocking")
	}
}

func TestHasBlockingErrors_WithError(t *testing.T) {
	errs := []domain.BPMNValidationError{{Severity: domain.SeverityWarning}, {Severity: domain.SeverityError}}
	if !hasBlockingErrors(errs) {
		t.Error("an error-severity entry should be blocking")
	}
}

// ─── executableProcesses (additional branches) ───────────────────────────

func TestExecutableProcesses_Single(t *testing.T) {
	defs := &bpmncore.BPMNDefinitions{Processes: []bpmncore.BPMNProcess{{ID: "P1"}}}
	got := executableProcesses(defs)
	if len(got) != 1 || got[0].ID != "P1" {
		t.Errorf("executableProcesses() = %+v, want [P1]", got)
	}
}

func TestExecutableProcesses_MultipleWithCalledElement(t *testing.T) {
	defs := &bpmncore.BPMNDefinitions{Processes: []bpmncore.BPMNProcess{
		{
			ID: "Root", IsExecutable: true,
			CallActivities: []bpmncore.BPMNCallActivity{{
				ID: "CA1",
				ExtensionElements: bpmncore.BPMNExtensionElements{
					ZeebeCalledElement: &bpmncore.ZeebeCalledElement{ProcessID: "Module"},
				},
			}},
		},
		{ID: "Module", IsExecutable: true},
		{ID: "NonExec", IsExecutable: false},
	}}
	got := executableProcesses(defs)
	if len(got) != 1 || got[0].ID != "Root" {
		t.Errorf("executableProcesses() = %+v, want only [Root]", got)
	}
}

// ─── resolveMainPlanName ──────────────────────────────────────────────────

func TestResolveMainPlanName_MainPropPresent(t *testing.T) {
	procs := []bpmncore.BPMNProcess{
		{Name: "A"},
		{Name: "B", ExtensionElements: bpmncore.BPMNExtensionElements{
			ZeebeProps: bpmncore.BPMNZeebeProperties{Items: []bpmncore.ZeebeProperty{{Name: "main", Value: "1"}}},
		}},
	}
	if got := resolveMainPlanName(procs); got != "B" {
		t.Errorf("resolveMainPlanName() = %q, want B", got)
	}
}

func TestResolveMainPlanName_FallsBackToFirstNonIgnored(t *testing.T) {
	procs := []bpmncore.BPMNProcess{
		*propProc("ignore", "true"),
		{Name: "Main2"},
	}
	procs[0].Name = "Ignored1"
	if got := resolveMainPlanName(procs); got != "Main2" {
		t.Errorf("resolveMainPlanName() = %q, want Main2", got)
	}
}

func TestResolveMainPlanName_AllIgnoredNoMainProp(t *testing.T) {
	procs := []bpmncore.BPMNProcess{*propProc("ignore", "true")}
	procs[0].Name = "X"
	if got := resolveMainPlanName(procs); got != "" {
		t.Errorf("resolveMainPlanName() = %q, want empty", got)
	}
}

// ─── compileIgnoredPool ───────────────────────────────────────────────────

func TestCompileIgnoredPool_HappyPath(t *testing.T) {
	proc := &bpmncore.BPMNProcess{
		ReceiveTasks: []bpmncore.BPMNReceiveTask{{ID: "Recv1"}},
		EndEvents:    []bpmncore.BPMNEvent{{ID: "End1"}},
		SequenceFlows: []bpmncore.BPMNSequenceFlow{
			{ID: "F1", SourceRef: "Recv1", TargetRef: "End1"},
		},
	}
	g := bpmncore.BuildGraph(proc)
	plan := compileIgnoredPool(proc, g, defaultStageTypes(), element.DefaultElementHandlers(), &bpmncore.BPMNDefinitions{})
	if !plan.Ignored {
		t.Error("expected Ignored=true")
	}
	if plan.TaskQueue != "" {
		t.Errorf("TaskQueue = %q, want empty", plan.TaskQueue)
	}
}

func TestCompileIgnoredPool_AmbiguousImplicitStartFallback(t *testing.T) {
	// Two disconnected nodes each with no incoming edge -> FindImplicitStart
	// cannot resolve a unique candidate -> CompileWithImplicitStart fails.
	proc := &bpmncore.BPMNProcess{
		UserTasks: []bpmncore.BPMNUserTask{{ID: "T1"}, {ID: "T2"}},
		EndEvents: []bpmncore.BPMNEvent{{ID: "E1"}, {ID: "E2"}},
		SequenceFlows: []bpmncore.BPMNSequenceFlow{
			{ID: "F1", SourceRef: "T1", TargetRef: "E1"},
			{ID: "F2", SourceRef: "T2", TargetRef: "E2"},
		},
	}
	proc.Name = "AmbiguousPool"
	g := bpmncore.BuildGraph(proc)
	plan := compileIgnoredPool(proc, g, defaultStageTypes(), element.DefaultElementHandlers(), &bpmncore.BPMNDefinitions{})
	if plan.Name != proc.Name {
		t.Errorf("Name = %q, want %q", plan.Name, proc.Name)
	}
	if !plan.Ignored {
		t.Error("expected Ignored=true")
	}
	if plan.TaskQueue != "" {
		t.Errorf("TaskQueue = %q, want empty", plan.TaskQueue)
	}
	if len(plan.Departments) != 0 {
		t.Errorf("Departments = %+v, want empty (zero-value fallback)", plan.Departments)
	}
	if len(plan.Execution.Steps) != 0 {
		t.Errorf("Execution.Steps = %+v, want empty (zero-value fallback)", plan.Execution.Steps)
	}
}

// ─── validateCallPoolConstraints ──────────────────────────────────────────

func TestValidateCallPoolConstraints_IgnoredPoolWithCallPool(t *testing.T) {
	plans := []*domain.CompiledPlan{{
		Name: "Ignored", Ignored: true,
		Execution: domain.ExecutionPlan{Steps: []domain.ExecutionStep{{CallPool: &domain.CallPoolStep{Pool: "other"}}}},
	}}
	err := validateCallPoolConstraints(plans, "Main")
	var vfe *domain.ValidationFailedError
	if !errors.As(err, &vfe) {
		t.Fatalf("expected ValidationFailedError; got %T: %v", err, err)
	}
	if len(vfe.Errors) != 1 || vfe.Errors[0].Code != domain.BPMNErrUnmatchedMessageFlow {
		t.Errorf("Errors = %+v, want a single UNMATCHED_MESSAGE_FLOW", vfe.Errors)
	}
}

func TestValidateCallPoolConstraints_MainPoolSkipped(t *testing.T) {
	plans := []*domain.CompiledPlan{{
		Name: "Main", Ignored: true,
		Execution: domain.ExecutionPlan{Steps: []domain.ExecutionStep{{CallPool: &domain.CallPoolStep{Pool: "other"}}}},
	}}
	if err := validateCallPoolConstraints(plans, "Main"); err != nil {
		t.Errorf("main pool should be exempt; got error: %v", err)
	}
}

func TestValidateCallPoolConstraints_NonIgnoredSkipped(t *testing.T) {
	plans := []*domain.CompiledPlan{{
		Name: "Foo", Ignored: false,
		Execution: domain.ExecutionPlan{Steps: []domain.ExecutionStep{{CallPool: &domain.CallPoolStep{Pool: "other"}}}},
	}}
	if err := validateCallPoolConstraints(plans, "Main"); err != nil {
		t.Errorf("non-ignored pool should be exempt; got error: %v", err)
	}
}

func TestValidateCallPoolConstraints_NoCallPool(t *testing.T) {
	plans := []*domain.CompiledPlan{{Name: "Ignored", Ignored: true}}
	if err := validateCallPoolConstraints(plans, "Main"); err != nil {
		t.Errorf("no call_pool anywhere; expected nil, got %v", err)
	}
}

// ─── collectMainElementIDs ────────────────────────────────────────────────

func TestCollectMainElementIDs_AllKinds(t *testing.T) {
	mainProc := &bpmncore.BPMNProcess{
		UserTasks:         []bpmncore.BPMNUserTask{{ID: "U1"}},
		SendTasks:         []bpmncore.BPMNSendTask{{ID: "S1"}},
		ReceiveTasks:      []bpmncore.BPMNReceiveTask{{ID: "R1"}},
		StartEvents:       []bpmncore.BPMNEvent{{ID: "St1"}},
		EndEvents:         []bpmncore.BPMNEvent{{ID: "E1"}},
		ExclusiveGateways: []bpmncore.BPMNGateway{{ID: "X1"}},
		SubProcesses:      []bpmncore.BPMNSubProcess{{ID: "Sp1"}},
		CallActivities:    []bpmncore.BPMNCallActivity{{ID: "C1"}},
	}
	got := collectMainElementIDs(mainProc)
	for _, id := range []string{"U1", "S1", "R1", "St1", "E1", "X1", "Sp1", "C1"} {
		if !got[id] {
			t.Errorf("collectMainElementIDs() missing %q", id)
		}
	}
}

// ─── collectIgnoredTaskProcs ──────────────────────────────────────────────

func TestCollectIgnoredTaskProcs_AllElementKinds(t *testing.T) {
	ignoredProc := bpmncore.BPMNProcess{
		ID:             "Ignored1",
		UserTasks:      []bpmncore.BPMNUserTask{{ID: "U1"}},
		SendTasks:      []bpmncore.BPMNSendTask{{ID: "S1"}},
		ReceiveTasks:   []bpmncore.BPMNReceiveTask{{ID: "R1"}},
		StartEvents:    []bpmncore.BPMNEvent{{ID: "St1"}},
		EndEvents:      []bpmncore.BPMNEvent{{ID: "E1"}},
		CallActivities: []bpmncore.BPMNCallActivity{{ID: "C1"}},
	}
	otherProc := bpmncore.BPMNProcess{ID: "Other1", UserTasks: []bpmncore.BPMNUserTask{{ID: "OU1"}}}
	defs := &bpmncore.BPMNDefinitions{Processes: []bpmncore.BPMNProcess{ignoredProc, otherProc}}
	ignoredProcIDs := map[string]bool{"Ignored1": true}

	got := collectIgnoredTaskProcs(defs, ignoredProcIDs)
	for _, id := range []string{"U1", "S1", "R1", "St1", "E1", "C1"} {
		if got[id] != "Ignored1" {
			t.Errorf("collectIgnoredTaskProcs()[%q] = %q, want Ignored1", id, got[id])
		}
	}
	if _, ok := got["OU1"]; ok {
		t.Error("non-ignored process should not contribute entries")
	}
}

// ─── correlateMessageFlows ────────────────────────────────────────────────

func TestCorrelateMessageFlows(t *testing.T) {
	defs := &bpmncore.BPMNDefinitions{Collaboration: &bpmncore.BPMNCollaboration{MessageFlows: []bpmncore.BPMNMessageFlow{
		{SourceRef: "MainSend", TargetRef: "IgnRecv"},    // main -> ignored
		{SourceRef: "IgnSend", TargetRef: "MainRecv"},    // ignored -> main
		{SourceRef: "IgnOther1", TargetRef: "IgnOther2"}, // matches neither
	}}}
	mainElems := map[string]bool{"MainSend": true, "MainRecv": true}
	ignoredTaskProc := map[string]string{"IgnRecv": "P1", "IgnSend": "P1", "IgnOther1": "P1", "IgnOther2": "P1"}

	mainToIgnored, ignoredToMain := correlateMessageFlows(defs, mainElems, ignoredTaskProc)
	if len(mainToIgnored) != 1 || mainToIgnored["MainSend"] != "P1" {
		t.Errorf("mainToIgnored = %v, want {MainSend: P1}", mainToIgnored)
	}
	if len(ignoredToMain) != 1 || ignoredToMain["IgnSend"] != "MainRecv" {
		t.Errorf("ignoredToMain = %v, want {IgnSend: MainRecv}", ignoredToMain)
	}
}

// ─── buildIgnoredGraphs ───────────────────────────────────────────────────

func TestBuildIgnoredGraphs(t *testing.T) {
	defs := &bpmncore.BPMNDefinitions{Processes: []bpmncore.BPMNProcess{
		{ID: "Ignored1", StartEvents: []bpmncore.BPMNEvent{{ID: "S1"}}},
		{ID: "Main1"},
	}}
	ignoredProcIDs := map[string]bool{"Ignored1": true}
	got := buildIgnoredGraphs(defs, ignoredProcIDs)
	if _, ok := got["Ignored1"]; !ok {
		t.Error("expected graph built for ignored process")
	}
	if _, ok := got["Main1"]; ok {
		t.Error("non-ignored process should not get a graph")
	}
}

// ─── findReturnTaskID ─────────────────────────────────────────────────────

func TestFindReturnTaskID_NilGraph(t *testing.T) {
	if got := findReturnTaskID(nil, "X", map[string]string{}, map[string]bool{}); got != "" {
		t.Errorf("findReturnTaskID(nil graph) = %q, want empty", got)
	}
}

func TestFindReturnTaskID_ImmediateHit(t *testing.T) {
	ig := &bpmncore.Graph{Outgoing: map[string][]string{}}
	ignoredToMain := map[string]string{"Recv1": "Main1"}
	used := map[string]bool{}
	got := findReturnTaskID(ig, "Recv1", ignoredToMain, used)
	if got != "Main1" {
		t.Errorf("findReturnTaskID() = %q, want Main1", got)
	}
	if !used["Main1"] {
		t.Error("expected used[Main1] to be marked true")
	}
}

func TestFindReturnTaskID_SkipsUsedContinuesBFS(t *testing.T) {
	ig := &bpmncore.Graph{Outgoing: map[string][]string{"Recv2": {"Send2"}}}
	ignoredToMain := map[string]string{"Recv2": "MainA", "Send2": "MainB"}
	used := map[string]bool{"MainA": true}
	got := findReturnTaskID(ig, "Recv2", ignoredToMain, used)
	if got != "MainB" {
		t.Errorf("findReturnTaskID() = %q, want MainB (should skip already-used MainA)", got)
	}
}

func TestFindReturnTaskID_NoneReachable(t *testing.T) {
	ig := &bpmncore.Graph{Outgoing: map[string][]string{"Recv3": {"Mid3"}}}
	got := findReturnTaskID(ig, "Recv3", map[string]string{}, map[string]bool{})
	if got != "" {
		t.Errorf("findReturnTaskID() = %q, want empty", got)
	}
}

// ─── buildElementToProcessName ────────────────────────────────────────────

func TestBuildElementToProcessName_AllKinds(t *testing.T) {
	procs := []bpmncore.BPMNProcess{{
		Name:              "P1",
		UserTasks:         []bpmncore.BPMNUserTask{{ID: "U1"}},
		SendTasks:         []bpmncore.BPMNSendTask{{ID: "S1"}},
		ReceiveTasks:      []bpmncore.BPMNReceiveTask{{ID: "R1"}},
		StartEvents:       []bpmncore.BPMNEvent{{ID: "St1"}},
		EndEvents:         []bpmncore.BPMNEvent{{ID: "E1"}},
		ParallelGateways:  []bpmncore.BPMNGateway{{ID: "PG1"}},
		ExclusiveGateways: []bpmncore.BPMNGateway{{ID: "XG1"}},
		InclusiveGateways: []bpmncore.BPMNGateway{{ID: "IG1"}},
		SubProcesses:      []bpmncore.BPMNSubProcess{{ID: "Sp1"}},
		CallActivities:    []bpmncore.BPMNCallActivity{{ID: "C1"}},
		BoundaryEvents:    []bpmncore.BPMNBoundaryEvent{{ID: "B1"}},
	}}
	got := buildElementToProcessName(procs)
	for _, id := range []string{"U1", "S1", "R1", "St1", "E1", "PG1", "XG1", "IG1", "Sp1", "C1", "B1"} {
		if got[id] != "P1" {
			t.Errorf("buildElementToProcessName()[%q] = %q, want P1", id, got[id])
		}
	}
}

// ─── buildMessageBridges ──────────────────────────────────────────────────

func TestBuildMessageBridges_NilCollaboration(t *testing.T) {
	got := buildMessageBridges(&bpmncore.BPMNProcess{}, &bpmncore.BPMNDefinitions{}, nil, nil)
	if got != nil {
		t.Errorf("buildMessageBridges() = %v, want nil", got)
	}
}

func TestBuildMessageBridges_RoundTrip(t *testing.T) {
	ignoredProc := bpmncore.BPMNProcess{
		ID:            "IgnoredProc",
		ReceiveTasks:  []bpmncore.BPMNReceiveTask{{ID: "IgnRecv"}},
		SendTasks:     []bpmncore.BPMNSendTask{{ID: "IgnSend"}},
		SequenceFlows: []bpmncore.BPMNSequenceFlow{{SourceRef: "IgnRecv", TargetRef: "IgnSend"}},
	}
	mainProc := &bpmncore.BPMNProcess{
		SendTasks: []bpmncore.BPMNSendTask{{ID: "MainSend"}},
		UserTasks: []bpmncore.BPMNUserTask{{ID: "MainReturn"}},
	}
	defs := &bpmncore.BPMNDefinitions{
		Processes: []bpmncore.BPMNProcess{ignoredProc},
		Collaboration: &bpmncore.BPMNCollaboration{MessageFlows: []bpmncore.BPMNMessageFlow{
			{ID: "MF1", SourceRef: "MainSend", TargetRef: "IgnRecv"},
			{ID: "MF2", SourceRef: "IgnSend", TargetRef: "MainReturn"},
		}},
	}
	ignoredProcIDs := map[string]bool{"IgnoredProc": true}
	participantNameByProcID := map[string]string{"IgnoredProc": "External"}

	bridges := buildMessageBridges(mainProc, defs, ignoredProcIDs, participantNameByProcID)
	if len(bridges) != 1 {
		t.Fatalf("expected 1 bridge; got %d: %+v", len(bridges), bridges)
	}
	b := bridges[0]
	if b.FromTaskID != "MainSend" {
		t.Errorf("FromTaskID = %q, want MainSend", b.FromTaskID)
	}
	if b.ToTaskID != "MainReturn" {
		t.Errorf("ToTaskID = %q, want MainReturn (round-trip)", b.ToTaskID)
	}
	if b.ParticipantName != "External" {
		t.Errorf("ParticipantName = %q, want External", b.ParticipantName)
	}
	if !strings.HasPrefix(b.SyntheticNodeID, "__ext__") || b.SyntheticNodeID == "__ext__" {
		t.Errorf("SyntheticNodeID = %q, want non-empty __ext__ prefixed value", b.SyntheticNodeID)
	}
}

func TestBuildMessageBridges_Terminal(t *testing.T) {
	ignoredProc := bpmncore.BPMNProcess{
		ID:            "IgnoredProc",
		ReceiveTasks:  []bpmncore.BPMNReceiveTask{{ID: "IgnRecv"}},
		SendTasks:     []bpmncore.BPMNSendTask{{ID: "IgnSend"}},
		SequenceFlows: []bpmncore.BPMNSequenceFlow{{SourceRef: "IgnRecv", TargetRef: "IgnSend"}},
	}
	mainProc := &bpmncore.BPMNProcess{
		SendTasks: []bpmncore.BPMNSendTask{{ID: "MainSend"}},
	}
	defs := &bpmncore.BPMNDefinitions{
		Processes: []bpmncore.BPMNProcess{ignoredProc},
		Collaboration: &bpmncore.BPMNCollaboration{MessageFlows: []bpmncore.BPMNMessageFlow{
			{ID: "MF1", SourceRef: "MainSend", TargetRef: "IgnRecv"},
		}},
	}
	ignoredProcIDs := map[string]bool{"IgnoredProc": true}
	participantNameByProcID := map[string]string{"IgnoredProc": "External"}

	bridges := buildMessageBridges(mainProc, defs, ignoredProcIDs, participantNameByProcID)
	if len(bridges) != 1 {
		t.Fatalf("expected 1 bridge; got %d: %+v", len(bridges), bridges)
	}
	if bridges[0].ToTaskID != "" {
		t.Errorf("ToTaskID = %q, want empty (terminal, one-way)", bridges[0].ToTaskID)
	}
}

// ─── collabImplicitStart ──────────────────────────────────────────────────

func TestCollabImplicitStart_HasStartEvents(t *testing.T) {
	proc := &bpmncore.BPMNProcess{StartEvents: []bpmncore.BPMNEvent{{ID: "S1"}}}
	if got := collabImplicitStart(proc, &bpmncore.Graph{}, &bpmncore.BPMNDefinitions{}, nil); got != "" {
		t.Errorf("collabImplicitStart() = %q, want empty when process has an explicit start event", got)
	}
}

func TestCollabImplicitStart_NilCollaborationDelegates(t *testing.T) {
	proc := &bpmncore.BPMNProcess{
		ReceiveTasks:  []bpmncore.BPMNReceiveTask{{ID: "R1"}},
		EndEvents:     []bpmncore.BPMNEvent{{ID: "E1"}},
		SequenceFlows: []bpmncore.BPMNSequenceFlow{{SourceRef: "R1", TargetRef: "E1"}},
	}
	g := bpmncore.BuildGraph(proc)
	got := collabImplicitStart(proc, g, &bpmncore.BPMNDefinitions{Collaboration: nil}, nil)
	if got != "R1" {
		t.Errorf("collabImplicitStart() = %q, want R1 (delegated to FindImplicitStart)", got)
	}
}

func TestCollabImplicitStart_SkipsIgnoredWithoutStartEvents(t *testing.T) {
	proc := &bpmncore.BPMNProcess{
		ReceiveTasks:  []bpmncore.BPMNReceiveTask{{ID: "R1"}},
		EndEvents:     []bpmncore.BPMNEvent{{ID: "E1"}},
		SequenceFlows: []bpmncore.BPMNSequenceFlow{{SourceRef: "R1", TargetRef: "E1"}},
	}
	g := bpmncore.BuildGraph(proc)
	ignoredNoStart := bpmncore.BPMNProcess{ID: "Ign1"} // no StartEvents -> skipped in loop
	defs := &bpmncore.BPMNDefinitions{
		Processes:     []bpmncore.BPMNProcess{ignoredNoStart},
		Collaboration: &bpmncore.BPMNCollaboration{},
	}
	got := collabImplicitStart(proc, g, defs, map[string]bool{"Ign1": true})
	if got != "R1" {
		t.Errorf("collabImplicitStart() = %q, want R1 (fallback after skipping)", got)
	}
}

func TestCollabImplicitStart_FoundViaMessageFlow(t *testing.T) {
	proc := &bpmncore.BPMNProcess{UserTasks: []bpmncore.BPMNUserTask{{ID: "MainTask"}}}
	g := bpmncore.BuildGraph(proc)
	ignoredProc := bpmncore.BPMNProcess{
		ID:            "Ign1",
		StartEvents:   []bpmncore.BPMNEvent{{ID: "IStart"}},
		SendTasks:     []bpmncore.BPMNSendTask{{ID: "ISend"}},
		SequenceFlows: []bpmncore.BPMNSequenceFlow{{SourceRef: "IStart", TargetRef: "ISend"}},
	}
	defs := &bpmncore.BPMNDefinitions{
		Processes: []bpmncore.BPMNProcess{ignoredProc},
		Collaboration: &bpmncore.BPMNCollaboration{MessageFlows: []bpmncore.BPMNMessageFlow{
			{SourceRef: "ISend", TargetRef: "MainTask"},
		}},
	}
	got := collabImplicitStart(proc, g, defs, map[string]bool{"Ign1": true})
	if got != "MainTask" {
		t.Errorf("collabImplicitStart() = %q, want MainTask", got)
	}
}

func TestCollabImplicitStart_NothingFoundFallsBack(t *testing.T) {
	proc := &bpmncore.BPMNProcess{
		ReceiveTasks:  []bpmncore.BPMNReceiveTask{{ID: "R1"}},
		EndEvents:     []bpmncore.BPMNEvent{{ID: "E1"}},
		SequenceFlows: []bpmncore.BPMNSequenceFlow{{SourceRef: "R1", TargetRef: "E1"}},
	}
	g := bpmncore.BuildGraph(proc)
	ignoredProc := bpmncore.BPMNProcess{ID: "Ign1", StartEvents: []bpmncore.BPMNEvent{{ID: "IStart"}}}
	defs := &bpmncore.BPMNDefinitions{
		Processes:     []bpmncore.BPMNProcess{ignoredProc},
		Collaboration: &bpmncore.BPMNCollaboration{},
	}
	got := collabImplicitStart(proc, g, defs, map[string]bool{"Ign1": true})
	if got != "R1" {
		t.Errorf("collabImplicitStart() = %q, want R1 (fallback to FindImplicitStart)", got)
	}
}

// ─── firstMessageTargetInProc ─────────────────────────────────────────────

func TestFirstMessageTargetInProc_PlainHit(t *testing.T) {
	ignoredProc := &bpmncore.BPMNProcess{
		StartEvents:   []bpmncore.BPMNEvent{{ID: "IStart"}},
		SendTasks:     []bpmncore.BPMNSendTask{{ID: "ISend"}},
		SequenceFlows: []bpmncore.BPMNSequenceFlow{{SourceRef: "IStart", TargetRef: "ISend"}},
	}
	proc := &bpmncore.BPMNProcess{UserTasks: []bpmncore.BPMNUserTask{{ID: "MainTask"}}}
	g := bpmncore.BuildGraph(proc)
	defs := &bpmncore.BPMNDefinitions{Collaboration: &bpmncore.BPMNCollaboration{MessageFlows: []bpmncore.BPMNMessageFlow{
		{SourceRef: "ISend", TargetRef: "MainTask"},
	}}}
	if got := firstMessageTargetInProc(ignoredProc, proc, g, defs); got != "MainTask" {
		t.Errorf("firstMessageTargetInProc() = %q, want MainTask", got)
	}
}

func TestFirstMessageTargetInProc_BoundaryEventReturnsHostTask(t *testing.T) {
	ignoredProc := &bpmncore.BPMNProcess{
		StartEvents:   []bpmncore.BPMNEvent{{ID: "IStart"}},
		SendTasks:     []bpmncore.BPMNSendTask{{ID: "ISend"}},
		SequenceFlows: []bpmncore.BPMNSequenceFlow{{SourceRef: "IStart", TargetRef: "ISend"}},
	}
	proc := &bpmncore.BPMNProcess{
		UserTasks: []bpmncore.BPMNUserTask{{ID: "HostTask"}},
		BoundaryEvents: []bpmncore.BPMNBoundaryEvent{{
			ID: "BE1", AttachedToRef: "HostTask",
			Message: &bpmncore.BPMNMessageEventDef{MessageRef: "Msg1"},
		}},
	}
	g := bpmncore.BuildGraph(proc)
	defs := &bpmncore.BPMNDefinitions{Collaboration: &bpmncore.BPMNCollaboration{MessageFlows: []bpmncore.BPMNMessageFlow{
		{SourceRef: "ISend", TargetRef: "BE1"},
	}}}
	if got := firstMessageTargetInProc(ignoredProc, proc, g, defs); got != "HostTask" {
		t.Errorf("firstMessageTargetInProc() = %q, want HostTask (boundary event host)", got)
	}
}

func TestFirstMessageTargetInProc_NoFlowFound(t *testing.T) {
	ignoredProc := &bpmncore.BPMNProcess{
		StartEvents:   []bpmncore.BPMNEvent{{ID: "IStart"}},
		SendTasks:     []bpmncore.BPMNSendTask{{ID: "ISend"}},
		SequenceFlows: []bpmncore.BPMNSequenceFlow{{SourceRef: "IStart", TargetRef: "ISend"}},
	}
	proc := &bpmncore.BPMNProcess{UserTasks: []bpmncore.BPMNUserTask{{ID: "MainTask"}}}
	g := bpmncore.BuildGraph(proc)
	defs := &bpmncore.BPMNDefinitions{Collaboration: &bpmncore.BPMNCollaboration{}}
	if got := firstMessageTargetInProc(ignoredProc, proc, g, defs); got != "" {
		t.Errorf("firstMessageTargetInProc() = %q, want empty", got)
	}
}

// ─── collaborationProcSets ────────────────────────────────────────────────

func TestCollaborationProcSets(t *testing.T) {
	defs := &bpmncore.BPMNDefinitions{
		Collaboration: &bpmncore.BPMNCollaboration{
			Participants: []bpmncore.BPMNParticipant{
				{ID: "P1", Name: "Main", ProcessRef: "Proc1"},
				{ID: "P2", Name: "Ext", ProcessRef: "Proc2"},
			},
		},
		Processes: []bpmncore.BPMNProcess{
			{ID: "Proc1"},
			*propProc("ignore", "true"),
		},
	}
	defs.Processes[1].ID = "Proc2"

	ignoredProcIDs, participantNameByProcID := collaborationProcSets(defs)
	if ignoredProcIDs["Proc1"] || !ignoredProcIDs["Proc2"] {
		t.Errorf("ignoredProcIDs = %v, want only Proc2", ignoredProcIDs)
	}
	if participantNameByProcID["Proc1"] != "Main" || participantNameByProcID["Proc2"] != "Ext" {
		t.Errorf("participantNameByProcID = %v", participantNameByProcID)
	}
}

// ─── validateCollaborationProcesses ───────────────────────────────────────

func TestValidateCollaborationProcesses_SkipsIgnored(t *testing.T) {
	c := NewCompiler()
	task := taskWithProps(map[string]string{"stage_type": "prep", "role": "ops", "default_user_ids": aliceUUID})
	mainProc := bpmncore.BPMNProcess{
		ID:   "Main",
		Name: "Main",
		LaneSet: bpmncore.BPMNLaneSet{Lanes: []bpmncore.BPMNLane{
			{ID: "L1", Name: "ops", FlowNodeRefs: []string{"S1", task.ID, "E1"}},
		}},
		StartEvents: []bpmncore.BPMNEvent{{ID: "S1"}},
		UserTasks:   []bpmncore.BPMNUserTask{*task},
		EndEvents:   []bpmncore.BPMNEvent{{ID: "E1"}},
		SequenceFlows: []bpmncore.BPMNSequenceFlow{
			{SourceRef: "S1", TargetRef: task.ID},
			{SourceRef: task.ID, TargetRef: "E1"},
		},
	}
	ignoredProc := *propProc("ignore", "true")
	ignoredProc.ID = "Ign"
	ignoredProc.Name = "Ignored"

	defs := &bpmncore.BPMNDefinitions{
		Processes:     []bpmncore.BPMNProcess{mainProc, ignoredProc},
		Collaboration: &bpmncore.BPMNCollaboration{},
	}
	errs := c.validateCollaborationProcesses("", defs, map[string]bool{"Ign": true}, map[string]string{})
	if len(errs) != 0 {
		t.Errorf("expected no errors (ignored process skipped, main process valid); got %v", errCodesOf(errs))
	}
}

// ─── compileCollaborationPlans ────────────────────────────────────────────

func TestCompileCollaborationPlans_SkipsUnmatchedParticipant(t *testing.T) {
	c := NewCompiler()
	defs := &bpmncore.BPMNDefinitions{
		Processes: []bpmncore.BPMNProcess{{
			ID: "Proc1", Name: "Main",
			StartEvents:   []bpmncore.BPMNEvent{{ID: "S1"}},
			EndEvents:     []bpmncore.BPMNEvent{{ID: "E1"}},
			SequenceFlows: []bpmncore.BPMNSequenceFlow{{SourceRef: "S1", TargetRef: "E1"}},
		}},
		Collaboration: &bpmncore.BPMNCollaboration{
			Participants: []bpmncore.BPMNParticipant{
				{ID: "Pg", ProcessRef: "Ghost"},
				{ID: "P1", ProcessRef: "Proc1"},
			},
		},
	}
	plans, err := c.compileCollaborationPlans(defs, map[string]bool{}, map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plans) != 1 || plans[0].Name != "Main" {
		t.Fatalf("expected 1 plan (ghost participant skipped); got %+v", plans)
	}
}

func TestCompileCollaborationPlans_IgnoredPoolQualifiesDepts(t *testing.T) {
	c := NewCompiler()
	ignoredProc := *propProc("ignore", "true")
	ignoredProc.ID = "IgnProc"
	ignoredProc.Name = "Ignored"
	ignoredProc.LaneSet = bpmncore.BPMNLaneSet{Lanes: []bpmncore.BPMNLane{{ID: "L1", Name: "ext", FlowNodeRefs: []string{"R1"}}}}
	ignoredProc.ReceiveTasks = []bpmncore.BPMNReceiveTask{{ID: "R1"}}
	ignoredProc.EndEvents = []bpmncore.BPMNEvent{{ID: "E1"}}
	ignoredProc.SequenceFlows = []bpmncore.BPMNSequenceFlow{{SourceRef: "R1", TargetRef: "E1"}}

	defs := &bpmncore.BPMNDefinitions{
		Processes: []bpmncore.BPMNProcess{ignoredProc},
		Collaboration: &bpmncore.BPMNCollaboration{
			Participants: []bpmncore.BPMNParticipant{{ID: "P1", ProcessRef: "IgnProc", Name: "Ignored"}},
		},
	}
	plans, err := c.compileCollaborationPlans(defs, map[string]bool{"IgnProc": true}, map[string]string{"IgnProc": "Ignored"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("expected 1 plan; got %d", len(plans))
	}
	if !plans[0].Ignored {
		t.Error("expected Ignored=true")
	}
	found := false
	for _, d := range plans[0].Departments {
		if d.ID == "Ignored/ext" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a department ID prefixed with plan name; got %+v", plans[0].Departments)
	}
}

func TestCompileCollaborationPlans_CompileErrorWrapped(t *testing.T) {
	c := NewCompiler()
	badProc := bpmncore.BPMNProcess{
		ID: "Bad", Name: "Bad",
		UserTasks: []bpmncore.BPMNUserTask{{ID: "T1"}, {ID: "T2"}},
		EndEvents: []bpmncore.BPMNEvent{{ID: "E1"}, {ID: "E2"}},
		SequenceFlows: []bpmncore.BPMNSequenceFlow{
			{SourceRef: "T1", TargetRef: "E1"},
			{SourceRef: "T2", TargetRef: "E2"},
		},
	}
	defs := &bpmncore.BPMNDefinitions{
		Processes: []bpmncore.BPMNProcess{badProc},
		Collaboration: &bpmncore.BPMNCollaboration{
			Participants: []bpmncore.BPMNParticipant{{ID: "P1", ProcessRef: "Bad"}},
		},
	}
	_, err := c.compileCollaborationPlans(defs, map[string]bool{}, map[string]string{})
	if err == nil {
		t.Fatal("expected error for ambiguous implicit start")
	}
	if !strings.Contains(err.Error(), `bpmn compile-collaboration participant "P1"`) {
		t.Errorf("error = %q, want to contain participant id", err.Error())
	}
}

// ─── buildMessageDefs ─────────────────────────────────────────────────────

func TestBuildMessageDefs(t *testing.T) {
	defs := &bpmncore.BPMNDefinitions{
		Processes: []bpmncore.BPMNProcess{
			{Name: "Main", SendTasks: []bpmncore.BPMNSendTask{{ID: "Send1"}}},
			{Name: "Other", ReceiveTasks: []bpmncore.BPMNReceiveTask{{ID: "Recv1"}}},
		},
		Messages: []bpmncore.BPMNMessage{{ID: "Msg1", Name: "notify"}},
		Collaboration: &bpmncore.BPMNCollaboration{MessageFlows: []bpmncore.BPMNMessageFlow{
			{ID: "MF1", SourceRef: "Send1", TargetRef: "Recv1", MessageRef: "Msg1"},
		}},
	}
	got := buildMessageDefs(defs)
	if len(got) != 1 {
		t.Fatalf("expected 1 message def; got %d", len(got))
	}
	if got[0].Name != "notify" || got[0].SourcePlan != "Main" || got[0].TargetPlan != "Other" {
		t.Errorf("got %+v", got[0])
	}
}

// ─── classifyParseError (via Compile) ─────────────────────────────────────

func TestCompile_ParseErrorClassification(t *testing.T) {
	c := New()

	t.Run("context canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := c.Compile(ctx, "<root><a/></root>")
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled; got %v", err)
		}
	})

	t.Run("forbidden XML", func(t *testing.T) {
		xmlData := `<?xml version="1.0"?><!DOCTYPE foo [<!ENTITY x "y">]><root/>`
		_, err := c.Compile(context.Background(), xmlData)
		if !errors.Is(err, domain.ErrForbiddenXML) {
			t.Errorf("expected ErrForbiddenXML; got %v", err)
		}
		if !errors.Is(err, domain.ErrMalformedBPMN) {
			t.Errorf("expected ErrMalformedBPMN; got %v", err)
		}
	})

	t.Run("generic malformed XML", func(t *testing.T) {
		xmlData := `<?xml version="1.0"?><bpmn:definitions xmlns:bpmn="` + nsBPMN + `" xmlns:zeebe="` + nsZeebe + `"><unclosed>`
		_, err := c.Compile(context.Background(), xmlData)
		if !errors.Is(err, domain.ErrMalformedBPMN) {
			t.Errorf("expected ErrMalformedBPMN; got %v", err)
		}
		if errors.Is(err, domain.ErrForbiddenXML) {
			t.Errorf("plain malformed XML should not be classified as forbidden; got %v", err)
		}
	})
}

// ─── Validate() entrypoint ────────────────────────────────────────────────

func TestValidate_MissingNamespace(t *testing.T) {
	c := New()
	errs, err := c.Validate(context.Background(), `<?xml version="1.0"?><foo/>`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasCodeIn(errs, domain.BPMNErrMissingNamespace) {
		t.Errorf("expected MISSING_NAMESPACE; got %v", errCodesOf(errs))
	}
}

// TestValidate_MissingNamespace_Collaboration confirms the namespace check in
// parse() runs as a raw-string scan before defs.Collaboration is ever
// consulted, so a collaboration document missing the zeebe namespace hits the
// same early MISSING_NAMESPACE return as a single-process one.
func TestValidate_MissingNamespace_Collaboration(t *testing.T) {
	c := New()
	bpmnXML := `<?xml version="1.0"?><bpmn:definitions xmlns:bpmn="` + nsBPMN + `"><bpmn:collaboration id="Collab1"/></bpmn:definitions>`
	errs, err := c.Validate(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasCodeIn(errs, domain.BPMNErrMissingNamespace) {
		t.Errorf("expected MISSING_NAMESPACE; got %v", errCodesOf(errs))
	}
}

func TestValidate_ParseErrorPropagates(t *testing.T) {
	c := New()
	xmlData := `<?xml version="1.0"?><bpmn:definitions xmlns:bpmn="` + nsBPMN + `" xmlns:zeebe="` + nsZeebe + `"><unclosed>`
	_, err := c.Validate(context.Background(), xmlData)
	if !errors.Is(err, domain.ErrMalformedBPMN) {
		t.Errorf("expected ErrMalformedBPMN; got %v", err)
	}
}

func TestValidate_MultipleExecutableProcessesNoCollaboration(t *testing.T) {
	bpmnXML := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="` + nsBPMN + `" xmlns:zeebe="` + nsZeebe + `">
  <bpmn:process id="P1" isExecutable="true"><bpmn:startEvent id="S1"/></bpmn:process>
  <bpmn:process id="P2" isExecutable="true"><bpmn:startEvent id="S2"/></bpmn:process>
</bpmn:definitions>`
	c := New()
	errs, err := c.Validate(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasCodeIn(errs, domain.BPMNErrMultipleProcesses) {
		t.Errorf("expected MULTIPLE_PROCESSES; got %v", errCodesOf(errs))
	}
}

func TestValidate_SingleProcess_Valid(t *testing.T) {
	bpmnXML := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="` + nsBPMN + `"
  xmlns:bpmndi="` + nsBPMNDI + `"
  xmlns:zeebe="` + nsZeebe + `"
  id="D1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="P1" name="Test" isExecutable="true">
    <bpmn:laneSet id="LS"><bpmn:lane id="L1" name="ops">
      <bpmn:flowNodeRef>S1</bpmn:flowNodeRef>
      <bpmn:flowNodeRef>T1</bpmn:flowNodeRef>
      <bpmn:flowNodeRef>E1</bpmn:flowNodeRef>
    </bpmn:lane></bpmn:laneSet>
    <bpmn:startEvent id="S1"><bpmn:outgoing>F1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:userTask id="T1" name="Task">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="` + aliceUUID + `"/>
      </bpmn:extensionElements>
      <bpmn:incoming>F1</bpmn:incoming><bpmn:outgoing>F2</bpmn:outgoing>
    </bpmn:userTask>
    <bpmn:endEvent id="E1"><bpmn:incoming>F2</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="F1" sourceRef="S1" targetRef="T1"/>
    <bpmn:sequenceFlow id="F2" sourceRef="T1" targetRef="E1"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BD">
    <bpmndi:BPMNPlane id="BP" bpmnElement="P1">
      <bpmndi:BPMNShape id="Sh_S1" bpmnElement="S1"/>
      <bpmndi:BPMNShape id="Sh_T1" bpmnElement="T1"/>
      <bpmndi:BPMNShape id="Sh_E1" bpmnElement="E1"/>
      <bpmndi:BPMNEdge id="Ed_F1" bpmnElement="F1"/>
      <bpmndi:BPMNEdge id="Ed_F2" bpmnElement="F2"/>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`
	c := New()
	errs, err := c.Validate(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(errs) != 0 {
		t.Errorf("expected no errors for valid process; got %v", errCodesOf(errs))
	}
}

func TestValidate_Collaboration_TerminalBridgeFiltersNoEndEvent(t *testing.T) {
	bpmnXML := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="` + nsBPMN + `"
  xmlns:zeebe="` + nsZeebe + `"
  id="Def1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:collaboration id="Collab1">
    <bpmn:participant id="P_sender" name="Sender" processRef="Proc_sender"/>
    <bpmn:participant id="P_ext" name="External" processRef="Proc_ext"/>
    <bpmn:messageFlow id="MF1" sourceRef="Task_send" targetRef="Task_ext_recv"/>
  </bpmn:collaboration>
  <bpmn:process id="Proc_sender" name="Sender" isExecutable="true">
    <bpmn:laneSet id="LS1">
      <bpmn:lane id="L1" name="ops">
        <bpmn:flowNodeRef>Start1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_send</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="Start1"><bpmn:outgoing>F1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:sendTask id="Task_send" name="Send">
      <bpmn:incoming>F1</bpmn:incoming>
    </bpmn:sendTask>
    <bpmn:sequenceFlow id="F1" sourceRef="Start1" targetRef="Task_send"/>
  </bpmn:process>
  <bpmn:process id="Proc_ext" name="External" isExecutable="true">
    <bpmn:extensionElements>
      <zeebe:properties>
        <zeebe:property name="ignore" value="true"/>
      </zeebe:properties>
    </bpmn:extensionElements>
    <bpmn:receiveTask id="Task_ext_recv" name="Recv"/>
  </bpmn:process>
</bpmn:definitions>`
	c := New()
	errs, err := c.Validate(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hasCodeIn(errs, domain.BPMNErrNoEndEvent) {
		t.Errorf("expected NO_END_EVENT to be filtered by the terminal message bridge; got %v", errCodesOf(errs))
	}
}

// ─── Compile() additional branches ────────────────────────────────────────

func TestCompile_MissingNamespace(t *testing.T) {
	c := New()
	_, err := c.Compile(context.Background(), `<?xml version="1.0"?><foo/>`)
	var vfe *domain.ValidationFailedError
	if !errors.As(err, &vfe) {
		t.Fatalf("expected ValidationFailedError; got %T: %v", err, err)
	}
	if !hasCodeIn(vfe.Errors, domain.BPMNErrMissingNamespace) {
		t.Errorf("expected MISSING_NAMESPACE; got %v", errCodesOf(vfe.Errors))
	}
}

func TestCompile_MultipleExecutableProcesses(t *testing.T) {
	bpmnXML := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="` + nsBPMN + `" xmlns:zeebe="` + nsZeebe + `">
  <bpmn:process id="P1" isExecutable="true"><bpmn:startEvent id="S1"/></bpmn:process>
  <bpmn:process id="P2" isExecutable="true"><bpmn:startEvent id="S2"/></bpmn:process>
</bpmn:definitions>`
	c := New()
	_, err := c.Compile(context.Background(), bpmnXML)
	var vfe *domain.ValidationFailedError
	if !errors.As(err, &vfe) {
		t.Fatalf("expected ValidationFailedError; got %T: %v", err, err)
	}
	if !hasCodeIn(vfe.Errors, domain.BPMNErrMultipleProcesses) {
		t.Errorf("expected MULTIPLE_PROCESSES; got %v", errCodesOf(vfe.Errors))
	}
}

func TestCompile_BlockingValidationErrors(t *testing.T) {
	bpmnXML := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="` + nsBPMN + `"
  xmlns:zeebe="` + nsZeebe + `"
  id="D1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="P1" name="Test" isExecutable="true">
    <bpmn:laneSet id="LS"><bpmn:lane id="L1" name="ops">
      <bpmn:flowNodeRef>S1</bpmn:flowNodeRef>
      <bpmn:flowNodeRef>T1</bpmn:flowNodeRef>
      <bpmn:flowNodeRef>E1</bpmn:flowNodeRef>
    </bpmn:lane></bpmn:laneSet>
    <bpmn:startEvent id="S1"><bpmn:outgoing>F1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:userTask id="T1" name="Task">
      <bpmn:extensionElements><zeebe:taskDefinition type="prep"/></bpmn:extensionElements>
      <bpmn:incoming>F1</bpmn:incoming><bpmn:outgoing>F2</bpmn:outgoing>
    </bpmn:userTask>
    <bpmn:endEvent id="E1"><bpmn:incoming>F2</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="F1" sourceRef="S1" targetRef="T1"/>
    <bpmn:sequenceFlow id="F2" sourceRef="T1" targetRef="E1"/>
  </bpmn:process>
</bpmn:definitions>`
	c := New()
	_, err := c.Compile(context.Background(), bpmnXML)
	var vfe *domain.ValidationFailedError
	if !errors.As(err, &vfe) {
		t.Fatalf("expected ValidationFailedError; got %T: %v", err, err)
	}
	if !hasCodeIn(vfe.Errors, domain.BPMNErrMissingAssignmentDefinition) {
		t.Errorf("expected MISSING_ASSIGNMENT_DEFINITION; got %v", errCodesOf(vfe.Errors))
	}
}

// TestCompile_BpmnCoreCompileError covers the case where structural validation
// passes but bpmncore.Compile still fails: a parallel gateway whose branches
// each terminate independently at their own end event has no matching join.
// checkSplitJoin treats "no join, but every branch terminates" as valid for
// ANY gateway kind, but handleSplitNoJoin only tolerates a missing join for
// exclusive gateways — a parallel split hits the "no matching join" error.
// Compile() must propagate that as a plain wrapped error, not a
// ValidationFailedError, since it happens after validation already passed.
func TestCompile_BpmnCoreCompileError(t *testing.T) {
	bpmnXML := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="` + nsBPMN + `"
  xmlns:bpmndi="` + nsBPMNDI + `"
  xmlns:zeebe="` + nsZeebe + `"
  id="D1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="P1" name="Test" isExecutable="true">
    <bpmn:laneSet id="LS"><bpmn:lane id="L1" name="ops">
      <bpmn:flowNodeRef>S1</bpmn:flowNodeRef>
      <bpmn:flowNodeRef>T1</bpmn:flowNodeRef>
      <bpmn:flowNodeRef>PGW</bpmn:flowNodeRef>
      <bpmn:flowNodeRef>End1</bpmn:flowNodeRef>
      <bpmn:flowNodeRef>End2</bpmn:flowNodeRef>
    </bpmn:lane></bpmn:laneSet>
    <bpmn:startEvent id="S1"><bpmn:outgoing>F1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:userTask id="T1" name="Task">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="` + aliceUUID + `"/>
      </bpmn:extensionElements>
      <bpmn:incoming>F1</bpmn:incoming><bpmn:outgoing>F2</bpmn:outgoing>
    </bpmn:userTask>
    <bpmn:parallelGateway id="PGW">
      <bpmn:incoming>F2</bpmn:incoming><bpmn:outgoing>F3</bpmn:outgoing><bpmn:outgoing>F4</bpmn:outgoing>
    </bpmn:parallelGateway>
    <bpmn:endEvent id="End1"><bpmn:incoming>F3</bpmn:incoming></bpmn:endEvent>
    <bpmn:endEvent id="End2"><bpmn:incoming>F4</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="F1" sourceRef="S1"  targetRef="T1"/>
    <bpmn:sequenceFlow id="F2" sourceRef="T1"  targetRef="PGW"/>
    <bpmn:sequenceFlow id="F3" sourceRef="PGW" targetRef="End1"/>
    <bpmn:sequenceFlow id="F4" sourceRef="PGW" targetRef="End2"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BD">
    <bpmndi:BPMNPlane id="BP" bpmnElement="P1">
      <bpmndi:BPMNShape id="Sh_S1"   bpmnElement="S1"/>
      <bpmndi:BPMNShape id="Sh_T1"   bpmnElement="T1"/>
      <bpmndi:BPMNShape id="Sh_PGW"  bpmnElement="PGW"/>
      <bpmndi:BPMNShape id="Sh_End1" bpmnElement="End1"/>
      <bpmndi:BPMNShape id="Sh_End2" bpmnElement="End2"/>
      <bpmndi:BPMNEdge id="Ed_F1" bpmnElement="F1"/>
      <bpmndi:BPMNEdge id="Ed_F2" bpmnElement="F2"/>
      <bpmndi:BPMNEdge id="Ed_F3" bpmnElement="F3"/>
      <bpmndi:BPMNEdge id="Ed_F4" bpmnElement="F4"/>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`
	c := New()

	valErrs, valErr := c.Validate(context.Background(), bpmnXML)
	if valErr != nil || len(valErrs) != 0 {
		t.Fatalf("expected clean validation (the compile failure must occur post-validation); got err=%v errs=%v", valErr, errCodesOf(valErrs))
	}

	_, err := c.Compile(context.Background(), bpmnXML)
	var vfe *domain.ValidationFailedError
	if errors.As(err, &vfe) {
		t.Fatalf("expected a plain wrapped compile error, not ValidationFailedError; got %v", vfe)
	}
	if err == nil {
		t.Fatal("expected an error from bpmncore.Compile for an unmatched parallel split")
	}
}

// ─── CompileCollaboration() additional branches ───────────────────────────

func TestCompileCollaboration_ParseErrorPropagates(t *testing.T) {
	c := New()
	xmlData := `<?xml version="1.0"?><bpmn:definitions xmlns:bpmn="` + nsBPMN + `" xmlns:zeebe="` + nsZeebe + `"><unclosed>`
	_, err := c.CompileCollaboration(context.Background(), xmlData)
	if !errors.Is(err, domain.ErrMalformedBPMN) {
		t.Errorf("expected ErrMalformedBPMN; got %v", err)
	}
}

func TestCompileCollaboration_MissingNamespace(t *testing.T) {
	c := New()
	_, err := c.CompileCollaboration(context.Background(), `<?xml version="1.0"?><foo/>`)
	var vfe *domain.ValidationFailedError
	if !errors.As(err, &vfe) {
		t.Fatalf("expected ValidationFailedError; got %T: %v", err, err)
	}
	if !hasCodeIn(vfe.Errors, domain.BPMNErrMissingNamespace) {
		t.Errorf("expected MISSING_NAMESPACE; got %v", errCodesOf(vfe.Errors))
	}
}

func TestCompileCollaboration_NoCollaborationElement(t *testing.T) {
	bpmnXML := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="` + nsBPMN + `" xmlns:zeebe="` + nsZeebe + `">
  <bpmn:process id="P1" isExecutable="true"><bpmn:startEvent id="S1"/></bpmn:process>
</bpmn:definitions>`
	c := New()
	_, err := c.CompileCollaboration(context.Background(), bpmnXML)
	var vfe *domain.ValidationFailedError
	if !errors.As(err, &vfe) {
		t.Fatalf("expected ValidationFailedError; got %T: %v", err, err)
	}
}

func TestCompileCollaboration_BlockingValidationErrors(t *testing.T) {
	bpmnXML := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="` + nsBPMN + `" xmlns:zeebe="` + nsZeebe + `">
  <bpmn:collaboration id="Collab1">
    <bpmn:participant id="P_a" processRef="Proc_a"/>
  </bpmn:collaboration>
  <bpmn:process id="Proc_a" name="A" isExecutable="true">
    <bpmn:laneSet id="LS"><bpmn:lane id="L" name="ops"><bpmn:flowNodeRef>T1</bpmn:flowNodeRef></bpmn:lane></bpmn:laneSet>
    <bpmn:userTask id="T1" name="T">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="` + aliceUUID + `"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
  </bpmn:process>
</bpmn:definitions>`
	c := New()
	_, err := c.CompileCollaboration(context.Background(), bpmnXML)
	var vfe *domain.ValidationFailedError
	if !errors.As(err, &vfe) {
		t.Fatalf("expected ValidationFailedError; got %T: %v", err, err)
	}
	if !hasCodeIn(vfe.Errors, domain.BPMNErrNoStartEvent) {
		t.Errorf("expected NO_START_EVENT; got %v", errCodesOf(vfe.Errors))
	}
}

const compileCollabSuccessBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="D1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:message id="Msg1" name="trigger"/>
  <bpmn:collaboration id="Collab1">
    <bpmn:participant id="P_a" name="A" processRef="Proc_a"/>
    <bpmn:participant id="P_b" name="B" processRef="Proc_b"/>
    <bpmn:messageFlow id="MF1" sourceRef="Task_a" targetRef="Start_b" messageRef="Msg1"/>
  </bpmn:collaboration>
  <bpmn:process id="Proc_a" name="A" isExecutable="true">
    <bpmn:extensionElements>
      <zeebe:properties><zeebe:property name="main" value="1"/></zeebe:properties>
    </bpmn:extensionElements>
    <bpmn:laneSet id="LSa"><bpmn:lane id="La" name="ops">
      <bpmn:flowNodeRef>Start_a</bpmn:flowNodeRef>
      <bpmn:flowNodeRef>Task_a</bpmn:flowNodeRef>
      <bpmn:flowNodeRef>End_a</bpmn:flowNodeRef>
    </bpmn:lane></bpmn:laneSet>
    <bpmn:startEvent id="Start_a"><bpmn:outgoing>Fa1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:sendTask id="Task_a" name="Send">
      <bpmn:incoming>Fa1</bpmn:incoming><bpmn:outgoing>Fa2</bpmn:outgoing>
    </bpmn:sendTask>
    <bpmn:endEvent id="End_a"><bpmn:incoming>Fa2</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="Fa1" sourceRef="Start_a" targetRef="Task_a"/>
    <bpmn:sequenceFlow id="Fa2" sourceRef="Task_a" targetRef="End_a"/>
  </bpmn:process>
  <bpmn:process id="Proc_b" name="B" isExecutable="true">
    <bpmn:laneSet id="LSb"><bpmn:lane id="Lb" name="fin">
      <bpmn:flowNodeRef>Start_b</bpmn:flowNodeRef>
      <bpmn:flowNodeRef>Task_b</bpmn:flowNodeRef>
      <bpmn:flowNodeRef>End_b</bpmn:flowNodeRef>
    </bpmn:lane></bpmn:laneSet>
    <bpmn:startEvent id="Start_b" name="Start">
      <bpmn:messageEventDefinition messageRef="Msg1"/>
      <bpmn:outgoing>Fb1</bpmn:outgoing>
    </bpmn:startEvent>
    <bpmn:userTask id="Task_b" name="Task">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="fin" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
      </bpmn:extensionElements>
      <bpmn:incoming>Fb1</bpmn:incoming><bpmn:outgoing>Fb2</bpmn:outgoing>
    </bpmn:userTask>
    <bpmn:endEvent id="End_b"><bpmn:incoming>Fb2</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="Fb1" sourceRef="Start_b" targetRef="Task_b"/>
    <bpmn:sequenceFlow id="Fb2" sourceRef="Task_b" targetRef="End_b"/>
  </bpmn:process>
</bpmn:definitions>`

func TestCompileCollaboration_SuccessWithMainProp(t *testing.T) {
	c := New()
	collab, err := c.CompileCollaboration(context.Background(), compileCollabSuccessBPMN)
	if err != nil {
		t.Fatalf("CompileCollaboration() error: %v", err)
	}
	if collab.MainPlan != "A" {
		t.Errorf("MainPlan = %q, want A", collab.MainPlan)
	}
	if len(collab.Plans) != 2 {
		t.Fatalf("expected 2 plans; got %d", len(collab.Plans))
	}
	if len(collab.Messages) != 1 || collab.Messages[0].Name != "trigger" {
		t.Errorf("expected 1 message named trigger; got %+v", collab.Messages)
	}
}

// fakeUserTaskHandler is a stand-in ElementHandler used only to verify that
// WithElementHandler overrides the registry entry for its NodeType.
type fakeUserTaskHandler struct{}

func (fakeUserTaskHandler) NodeType() bpmncore.FlowNodeType { return bpmncore.NodeTypeUserTask }
func (fakeUserTaskHandler) Validate(string, *bpmncore.BPMNProcess, *bpmncore.Graph, *bpmncore.BPMNDefinitions, map[string]bpmncore.StageTypeHandler, map[bpmncore.FlowNodeType]bpmncore.ElementHandler) []domain.BPMNValidationError {
	return nil
}
func (fakeUserTaskHandler) Compile(string, *bpmncore.CompileState) error { return nil }

func TestCompiler_WithElementHandler(t *testing.T) {
	fake := fakeUserTaskHandler{}
	c := NewCompiler(WithElementHandler(fake))
	got, ok := c.elements[bpmncore.NodeTypeUserTask]
	if !ok {
		t.Fatal("expected NodeTypeUserTask handler to be registered")
	}
	if _, ok := got.(fakeUserTaskHandler); !ok {
		t.Errorf("elements[NodeTypeUserTask] = %T, want fakeUserTaskHandler (WithElementHandler did not override the default)", got)
	}
}

func TestStepsHaveCallPool_NestedParallel(t *testing.T) {
	steps := []domain.ExecutionStep{
		{
			Parallel: []domain.ParallelBranch{
				{DeptID: "a", Steps: []domain.ExecutionStep{{CallPool: &domain.CallPoolStep{Pool: "Other"}}}},
			},
		},
	}
	if !stepsHaveCallPool(steps) {
		t.Error("expected stepsHaveCallPool to detect a CallPool nested inside a parallel branch")
	}
}

func TestStepsHaveCallPool_InSubWorkflow(t *testing.T) {
	steps := []domain.ExecutionStep{
		{
			SubWorkflow: &domain.SubWorkflowStep{
				Plan: domain.ExecutionPlan{
					Steps: []domain.ExecutionStep{{CallPool: &domain.CallPoolStep{Pool: "Other"}}},
				},
			},
		},
	}
	if !stepsHaveCallPool(steps) {
		t.Error("expected stepsHaveCallPool to detect a CallPool nested inside a SubWorkflow step")
	}
}

// ─── resolveMainPlanName / collabImplicitStart / Hash ─────────────────────

func TestResolveMainPlanName_WithMainZeebeProperty(t *testing.T) {
	procs := []bpmncore.BPMNProcess{
		{Name: "Pool_A"},
		{
			Name: "Pool_B",
			ExtensionElements: bpmncore.BPMNExtensionElements{
				ZeebeProps: bpmncore.BPMNZeebeProperties{
					Items: []bpmncore.ZeebeProperty{{Name: "main", Value: "1"}},
				},
			},
		},
	}
	if got := resolveMainPlanName(procs); got != "Pool_B" {
		t.Errorf("resolveMainPlanName() = %q, want %q (explicit main=1 marker takes priority over process order)", got, "Pool_B")
	}
}

func TestCollabImplicitStart_NoCollaboration(t *testing.T) {
	proc := &bpmncore.BPMNProcess{}
	g := &bpmncore.Graph{
		Outgoing: map[string][]string{"T1": {"T2"}},
		Incoming: map[string][]string{"T2": {"T1"}},
		NodeType: map[string]bpmncore.FlowNodeType{
			"T1": bpmncore.NodeTypeUserTask,
			"T2": bpmncore.NodeTypeUserTask,
		},
	}
	defs := &bpmncore.BPMNDefinitions{}

	got := collabImplicitStart(proc, g, defs, map[string]bool{})
	if got != "T1" {
		t.Errorf("collabImplicitStart() = %q, want %q (fall back to FindImplicitStart when Collaboration is nil)", got, "T1")
	}
}

func TestHash_MultiProcessBPMN_ReturnsError(t *testing.T) {
	bpmnXML := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="` + nsBPMN + `" xmlns:zeebe="` + nsZeebe + `">
  <bpmn:process id="P1" isExecutable="true"><bpmn:startEvent id="S1"/></bpmn:process>
  <bpmn:process id="P2" isExecutable="true"><bpmn:startEvent id="S2"/></bpmn:process>
</bpmn:definitions>`
	c := New()
	_, err := c.Hash(context.Background(), bpmnXML)
	if err == nil {
		t.Fatal("expected an error for a BPMN document with more than one process")
	}
}
