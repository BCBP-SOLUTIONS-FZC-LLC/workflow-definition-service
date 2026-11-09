package element

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
)

const compCoreIAMDept = "018e1f2a-0000-7000-8000-000000000099"

func compCoreChain(ids ...string) []bpmncore.BPMNSequenceFlow {
	flows := make([]bpmncore.BPMNSequenceFlow, 0, len(ids)-1)
	for i := 0; i+1 < len(ids); i++ {
		flows = append(flows, bpmncore.BPMNSequenceFlow{ID: fmt.Sprintf("flow_%s_%s", ids[i], ids[i+1]), SourceRef: ids[i], TargetRef: ids[i+1]})
	}
	return flows
}

func compCoreFlow(source, target string) bpmncore.BPMNSequenceFlow {
	return bpmncore.BPMNSequenceFlow{ID: "flow_" + source + "_" + target, SourceRef: source, TargetRef: target}
}

func compCoreCallExt(processID string) bpmncore.BPMNExtensionElements {
	return bpmncore.BPMNExtensionElements{ZeebeCalledElement: &bpmncore.ZeebeCalledElement{ProcessID: processID}}
}

// compCoreCallerProcess is a process "Root" whose only work is one call activity "CA".
func compCoreCallerProcess(ext bpmncore.BPMNExtensionElements, lanes ...bpmncore.BPMNLane) *bpmncore.BPMNProcess {
	return &bpmncore.BPMNProcess{
		ID:             "Root",
		Name:           "Root",
		LaneSet:        bpmncore.BPMNLaneSet{Lanes: lanes},
		StartEvents:    []bpmncore.BPMNEvent{{ID: "rootStart"}},
		EndEvents:      []bpmncore.BPMNEvent{{ID: "rootEnd"}},
		CallActivities: []bpmncore.BPMNCallActivity{{ID: "CA", Name: "Call", ExtensionElements: ext}},
		SequenceFlows:  compCoreChain("rootStart", "CA", "rootEnd"),
	}
}

// compCoreNoJoinBody has a parallel split whose two branches end separately, so it cannot compile.
func compCoreNoJoinBody(id string) *bpmncore.BPMNProcess {
	return &bpmncore.BPMNProcess{
		ID:               id,
		Name:             id,
		StartEvents:      []bpmncore.BPMNEvent{{ID: id + "Start"}},
		ParallelGateways: []bpmncore.BPMNGateway{{ID: "G"}},
		UserTasks:        []bpmncore.BPMNUserTask{{ID: "T1"}, {ID: "T2"}},
		EndEvents:        []bpmncore.BPMNEvent{{ID: "E1"}, {ID: "E2"}},
		SequenceFlows: []bpmncore.BPMNSequenceFlow{
			compCoreFlow(id+"Start", "G"), compCoreFlow("G", "T1"), compCoreFlow("G", "T2"),
			compCoreFlow("T1", "E1"), compCoreFlow("T2", "E2"),
		},
	}
}

// compCoreOverBudgetBody is a chain of more user tasks than a plan may hold stages.
func compCoreOverBudgetBody(id string) *bpmncore.BPMNProcess {
	ids := []string{id + "Start"}
	var tasks []bpmncore.BPMNUserTask
	for i := 0; i < bpmncore.StageBudget+2; i++ {
		taskID := fmt.Sprintf("task%d", i)
		ids = append(ids, taskID)
		tasks = append(tasks, bpmncore.BPMNUserTask{ID: taskID})
	}
	ids = append(ids, id+"End")
	return &bpmncore.BPMNProcess{
		ID:            id,
		Name:          id,
		StartEvents:   []bpmncore.BPMNEvent{{ID: id + "Start"}},
		UserTasks:     tasks,
		EndEvents:     []bpmncore.BPMNEvent{{ID: id + "End"}},
		SequenceFlows: compCoreChain(ids...),
	}
}

func compCoreAsSubProcess(id, name string, body *bpmncore.BPMNProcess) bpmncore.BPMNSubProcess {
	return bpmncore.BPMNSubProcess{
		ID:               id,
		Name:             name,
		StartEvents:      body.StartEvents,
		EndEvents:        body.EndEvents,
		ParallelGateways: body.ParallelGateways,
		UserTasks:        body.UserTasks,
		SequenceFlows:    body.SequenceFlows,
	}
}

func compCoreState(proc *bpmncore.BPMNProcess, defs *bpmncore.BPMNDefinitions) *bpmncore.CompileState {
	g := bpmncore.BuildGraph(proc)
	backEdges := map[[2]string]bool{}
	if len(proc.StartEvents) > 0 {
		backEdges = bpmncore.ProcessBackEdges(proc, g, proc.StartEvents[0].ID)
	}
	return bpmncore.NewCompileState(proc, g, bpmncore.BuildLaneLabels(proc), bpmncore.BuildCondExprs(proc), backEdges,
		map[string]bpmncore.StageTypeHandler{}, DefaultElementHandlers(), defs)
}

func compCoreCompile(t *testing.T, root *bpmncore.BPMNProcess, defs *bpmncore.BPMNDefinitions) (*dsl.CompiledPlan, []*dsl.CompiledPlan) {
	t.Helper()
	plan, called, err := bpmncore.Compile(root, bpmncore.BuildGraph(root), map[string]bpmncore.StageTypeHandler{}, DefaultElementHandlers(), defs)
	if err != nil {
		t.Fatalf("Compile() error = %v, want nil", err)
	}
	return plan, called
}

func TestCallActivityHandler_Compile_CalledProcessNotFound_ReturnsErrorNamingTheProcess(t *testing.T) {
	cases := []struct {
		name    string
		ext     bpmncore.BPMNExtensionElements
		wantErr string
	}{
		{
			name:    "no calledElement",
			ext:     bpmncore.BPMNExtensionElements{},
			wantErr: `callActivity "CA": called process "" not found`,
		},
		{
			name:    "calledElement with an empty process id",
			ext:     compCoreCallExt(""),
			wantErr: `callActivity "CA": called process "" not found`,
		},
		{
			name:    "calledElement naming a process that is not defined",
			ext:     compCoreCallExt("Missing"),
			wantErr: `callActivity "CA": called process "Missing" not found`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := compCoreCallerProcess(tc.ext)
			cs := compCoreState(root, &bpmncore.BPMNDefinitions{Processes: []bpmncore.BPMNProcess{*root}})

			err := CallActivityHandler{}.Compile("CA", cs)

			if err == nil || err.Error() != tc.wantErr {
				t.Fatalf("Compile() error = %v, want %q", err, tc.wantErr)
			}
			if got := cs.StepsCount(); got != 0 {
				t.Errorf("StepsCount() = %d, want 0: no step may be appended for an unresolved call", got)
			}
			if plans := cs.Called.All(); len(plans) != 0 {
				t.Errorf("called plans = %d, want none", len(plans))
			}
		})
	}
}

func TestCallActivityHandler_Compile_CalledProcessFails_WrapsTheInnerError(t *testing.T) {
	cases := []struct {
		name       string
		module     *bpmncore.BPMNProcess
		wantErr    string
		wantBudget bool
	}{
		{
			name:    "split gateway with no join",
			module:  compCoreNoJoinBody("Module"),
			wantErr: `callActivity "CA": split gateway "G" has no matching join`,
		},
		{
			name:       "more stages than the budget allows",
			module:     compCoreOverBudgetBody("Module"),
			wantErr:    `callActivity "CA": ` + bpmncore.ErrStageBudget.Error(),
			wantBudget: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := compCoreCallerProcess(compCoreCallExt("Module"))
			cs := compCoreState(root, &bpmncore.BPMNDefinitions{Processes: []bpmncore.BPMNProcess{*root, *tc.module}})

			err := CallActivityHandler{}.Compile("CA", cs)

			if err == nil || err.Error() != tc.wantErr {
				t.Fatalf("Compile() error = %v, want %q", err, tc.wantErr)
			}
			if got := errors.Is(err, bpmncore.ErrStageBudget); got != tc.wantBudget {
				t.Errorf("errors.Is(err, ErrStageBudget) = %v, want %v", got, tc.wantBudget)
			}
			if got := cs.StepsCount(); got != 0 {
				t.Errorf("StepsCount() = %d, want 0: a failed call appends no step", got)
			}
			if plans := cs.Called.All(); len(plans) != 0 {
				t.Errorf("called plans = %d, want none", len(plans))
			}
		})
	}
}

func TestCompile_ProcessThatCallsItself_Terminates(t *testing.T) {
	root := &bpmncore.BPMNProcess{
		ID:             "P",
		Name:           "P",
		StartEvents:    []bpmncore.BPMNEvent{{ID: "start"}},
		EndEvents:      []bpmncore.BPMNEvent{{ID: "end"}},
		CallActivities: []bpmncore.BPMNCallActivity{{ID: "CA", Name: "Again", ExtensionElements: compCoreCallExt("P")}},
		SequenceFlows:  compCoreChain("start", "CA", "end"),
	}
	defs := &bpmncore.BPMNDefinitions{Processes: []bpmncore.BPMNProcess{*root}}

	plan, called := compCoreCompile(t, root, defs)

	if len(plan.Execution.Steps) != 1 || plan.Execution.Steps[0].CallPlan == nil {
		t.Fatalf("root steps = %+v, want one call step", plan.Execution.Steps)
	}
	if got := plan.Execution.Steps[0].CallPlan; got.NodeID != "CA" || got.Plan != "P" {
		t.Errorf("root call = {NodeID:%q Plan:%q}, want {CA P}", got.NodeID, got.Plan)
	}
	if len(called) != 1 {
		t.Fatalf("called plans = %v, want exactly one (the compile must stop)", called)
	}
}

func TestCompile_CallActivity_DefaultDepartmentBindingNeedsExactlyOneOpenSlot(t *testing.T) {
	front := bpmncore.BPMNLane{ID: "front", Name: "Front", FlowNodeRefs: []string{"CA"}}
	opsOpen := bpmncore.BPMNLane{ID: "ops", Name: "Ops", FlowNodeRefs: []string{"T1"}}
	opsBound := bpmncore.BPMNLane{
		ID: "ops", Name: "Ops", FlowNodeRefs: []string{"T1"},
		ExtensionElements: bpmncore.BPMNExtensionElements{ZeebeProps: bpmncore.BPMNZeebeProperties{
			Items: []bpmncore.ZeebeProperty{{Name: "dept_id", Value: compCoreIAMDept}},
		}},
	}
	engOpen := bpmncore.BPMNLane{ID: "eng", Name: "Eng", FlowNodeRefs: []string{"T2"}}
	deptsOps := &bpmncore.ZeebeIOMapping{Inputs: []bpmncore.ZeebeIOVar{{Source: `={"Ops":"Front"}`, Target: bpmncore.CallInputDepts}}}

	cases := []struct {
		name      string
		lanes     []bpmncore.BPMNLane
		mapping   *bpmncore.ZeebeIOMapping
		wantDepts map[string]string
	}{
		{name: "no open slot", lanes: []bpmncore.BPMNLane{opsBound}, wantDepts: nil},
		{name: "one open slot is bound to the caller's lane", lanes: []bpmncore.BPMNLane{opsOpen}, wantDepts: map[string]string{"Ops": "Front"}},
		{name: "two open slots and no Depts", lanes: []bpmncore.BPMNLane{opsOpen, engOpen}, wantDepts: nil},
		{name: "two open slots and Depts binds one", lanes: []bpmncore.BPMNLane{opsOpen, engOpen}, mapping: deptsOps, wantDepts: map[string]string{"Ops": "Front"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			module := &bpmncore.BPMNProcess{
				ID:          "Module",
				Name:        "Module",
				LaneSet:     bpmncore.BPMNLaneSet{Lanes: tc.lanes},
				StartEvents: []bpmncore.BPMNEvent{{ID: "mStart"}},
				EndEvents:   []bpmncore.BPMNEvent{{ID: "mEnd"}},
				UserTasks:   []bpmncore.BPMNUserTask{{ID: "T1"}, {ID: "T2"}},
			}
			if len(tc.lanes) == 1 {
				module.UserTasks = module.UserTasks[:1]
				module.SequenceFlows = compCoreChain("mStart", "T1", "mEnd")
			} else {
				module.SequenceFlows = compCoreChain("mStart", "T1", "T2", "mEnd")
			}
			ext := compCoreCallExt("Module")
			ext.IOMapping = tc.mapping
			root := compCoreCallerProcess(ext, front)
			defs := &bpmncore.BPMNDefinitions{Processes: []bpmncore.BPMNProcess{*root, *module}}

			plan, _ := compCoreCompile(t, root, defs)

			if len(plan.Execution.Steps) != 1 || plan.Execution.Steps[0].CallPlan == nil {
				t.Fatalf("steps = %+v, want one call step", plan.Execution.Steps)
			}
			got := plan.Execution.Steps[0].CallPlan.Departments
			if (got == nil) != (tc.wantDepts == nil) || !maps.Equal(got, tc.wantDepts) {
				t.Errorf("Departments = %#v, want %#v", got, tc.wantDepts)
			}
		})
	}
}

func TestCompile_EmptySubProcessBody_AppendsAnEmptySubWorkflowAndLeavesItsSegmentUnmarked(t *testing.T) {
	root := &bpmncore.BPMNProcess{
		ID:   "Root",
		Name: "Root",
		LaneSet: bpmncore.BPMNLaneSet{Lanes: []bpmncore.BPMNLane{
			{ID: "ops", Name: "Ops", FlowNodeRefs: []string{"UT", "SP"}},
		}},
		StartEvents: []bpmncore.BPMNEvent{{ID: "start"}},
		EndEvents:   []bpmncore.BPMNEvent{{ID: "end"}},
		UserTasks:   []bpmncore.BPMNUserTask{{ID: "UT", Name: "Review"}},
		SubProcesses: []bpmncore.BPMNSubProcess{{
			ID:            "SP",
			Name:          "Nothing inside",
			StartEvents:   []bpmncore.BPMNEvent{{ID: "spStart"}},
			EndEvents:     []bpmncore.BPMNEvent{{ID: "spEnd"}},
			SequenceFlows: compCoreChain("spStart", "spEnd"),
		}},
		BoundaryEvents: []bpmncore.BPMNBoundaryEvent{{ID: "B", AttachedToRef: "UT", Timer: &bpmncore.BPMNTimerDef{Duration: "PT1H"}}},
		SequenceFlows:  append(compCoreChain("start", "UT", "SP", "end"), compCoreFlow("B", "SP")),
	}
	defs := &bpmncore.BPMNDefinitions{Processes: []bpmncore.BPMNProcess{*root}}

	plan, _ := compCoreCompile(t, root, defs)

	var sub *dsl.SubWorkflowStep
	for _, step := range plan.Execution.Steps {
		if step.SubWorkflow != nil {
			sub = step.SubWorkflow
		}
	}
	if sub == nil {
		t.Fatalf("steps = %+v, want a SubWorkflow step for the empty sub-process", plan.Execution.Steps)
	}
	if sub.NodeID != "SP" || sub.Name != "Nothing inside" || len(sub.Plan.Steps) != 0 {
		t.Errorf("SubWorkflow = {NodeID:%q Name:%q steps:%d}, want {SP \"Nothing inside\" 0}", sub.NodeID, sub.Name, len(sub.Plan.Steps))
	}
	if len(plan.Departments) != 1 || plan.Departments[0].ID != "Ops" {
		t.Fatalf("departments = %+v, want only Ops", plan.Departments)
	}
	stages := plan.Departments[0].Stages
	if len(stages) != 1 || stages[0].BoundaryTimer == nil {
		t.Fatalf("Ops stages = %+v, want the review task with its timer", stages)
	}
	if got := stages[0].BoundaryTimer; got.TargetDept != "Ops" || got.Terminates {
		t.Errorf("timer target = {%q terminates:%v}, want {Ops false}: the empty sub-process has no segment, so its lane is used", got.TargetDept, got.Terminates)
	}
}

func TestSubProcessHandler_Compile_BodyFails_WrapsTheInnerErrorAndAppendsNothing(t *testing.T) {
	cases := []struct {
		name       string
		body       *bpmncore.BPMNProcess
		wantErr    string
		wantBudget bool
	}{
		{
			name:    "split gateway with no join",
			body:    compCoreNoJoinBody("sp"),
			wantErr: `subprocess "SP": split gateway "G" has no matching join`,
		},
		{
			name:       "more stages than the budget allows",
			body:       compCoreOverBudgetBody("sp"),
			wantErr:    `subprocess "SP": ` + bpmncore.ErrStageBudget.Error(),
			wantBudget: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := &bpmncore.BPMNProcess{
				ID:            "Root",
				Name:          "Root",
				StartEvents:   []bpmncore.BPMNEvent{{ID: "start"}},
				EndEvents:     []bpmncore.BPMNEvent{{ID: "end"}},
				SubProcesses:  []bpmncore.BPMNSubProcess{compCoreAsSubProcess("SP", "Inner", tc.body)},
				SequenceFlows: compCoreChain("start", "SP", "end"),
			}
			cs := compCoreState(root, &bpmncore.BPMNDefinitions{Processes: []bpmncore.BPMNProcess{*root}})
			cs.AppendStep(dsl.ExecutionStep{Sequential: []string{"earlier"}})

			err := SubProcessHandler{}.Compile("SP", cs)

			if err == nil || err.Error() != tc.wantErr {
				t.Fatalf("Compile() error = %v, want %q", err, tc.wantErr)
			}
			if got := errors.Is(err, bpmncore.ErrStageBudget); got != tc.wantBudget {
				t.Errorf("errors.Is(err, ErrStageBudget) = %v, want %v", got, tc.wantBudget)
			}
			if steps := cs.CollectedSteps(); len(steps) != 1 || !slices.Equal(steps[0].Sequential, []string{"earlier"}) {
				t.Errorf("parent steps = %+v, want only the earlier step", steps)
			}
		})
	}
}
