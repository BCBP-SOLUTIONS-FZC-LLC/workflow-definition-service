package validator

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func compValChain(n int) *bpmncore.Graph {
	g := &bpmncore.Graph{Outgoing: map[string][]string{}}
	for i := 0; i < n-1; i++ {
		g.Outgoing[fmt.Sprintf("n%d", i)] = []string{fmt.Sprintf("n%d", i+1)}
	}
	return g
}

func TestValidateMaxDepth_StartEventSelection(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		nodes         int
		starts        []string
		implicitStart string
		wantNode      string
	}{
		{name: "no start event and no implicit start skips the check", nodes: maxPathDepth + 5},
		{name: "implicit start is measured when there is no start event", nodes: maxPathDepth + 1, implicitStart: "n0", wantNode: "n0"},
		{name: "first start event is the one measured", nodes: maxPathDepth + 1, starts: []string{"n0", "spare"}, wantNode: "n0"},
		{name: "start event wins over the implicit start", nodes: maxPathDepth + 1, starts: []string{"n0"}, implicitStart: "spare", wantNode: "n0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			proc := &bpmncore.BPMNProcess{}
			for _, id := range tc.starts {
				proc.StartEvents = append(proc.StartEvents, bpmncore.BPMNEvent{ID: id})
			}

			errs := validateMaxDepthFrom(proc, compValChain(tc.nodes), nil, tc.implicitStart)

			if tc.wantNode == "" {
				assert.Empty(t, errs)
				return
			}
			require.Len(t, errs, 1)
			assert.Equal(t, domain.BPMNErrMaxDepthExceeded, errs[0].Code)
			assert.Equal(t, tc.wantNode, errs[0].NodeID)
			assert.Equal(t, domain.SeverityError, errs[0].Severity)
			assert.Contains(t, errs[0].Message, fmt.Sprint(maxPathDepth))
		})
	}
}

func compValDrawnProcess() (*bpmncore.BPMNProcess, []string, []string) {
	proc := &bpmncore.BPMNProcess{
		StartEvents:       []bpmncore.BPMNEvent{{ID: "S"}},
		EndEvents:         []bpmncore.BPMNEvent{{ID: "E"}},
		UserTasks:         []bpmncore.BPMNUserTask{{ID: "U"}},
		SendTasks:         []bpmncore.BPMNSendTask{{ID: "SN"}},
		ReceiveTasks:      []bpmncore.BPMNReceiveTask{{ID: "RC"}},
		ParallelGateways:  []bpmncore.BPMNGateway{{ID: "P"}},
		ExclusiveGateways: []bpmncore.BPMNGateway{{ID: "X"}},
		InclusiveGateways: []bpmncore.BPMNGateway{{ID: "I"}},
		SubProcesses:      []bpmncore.BPMNSubProcess{{ID: "SP"}},
		CallActivities:    []bpmncore.BPMNCallActivity{{ID: "CA"}},
		BoundaryEvents:    []bpmncore.BPMNBoundaryEvent{{ID: "BE"}},
		SequenceFlows: []bpmncore.BPMNSequenceFlow{
			{ID: "F1", SourceRef: "S", TargetRef: "U"},
			{ID: "F2", SourceRef: "U", TargetRef: "E"},
		},
	}
	shapes := []string{"S", "E", "U", "SN", "RC", "P", "X", "I", "SP", "CA", "BE"}
	edges := []string{"F1", "F2"}
	return proc, shapes, edges
}

func compValDiagram(shapes, edges []string) bpmncore.BPMNDiagram {
	var d bpmncore.BPMNDiagram
	for _, id := range shapes {
		d.Plane.Shapes = append(d.Plane.Shapes, bpmncore.BPMNShape{BPMNElement: id})
	}
	for _, id := range edges {
		d.Plane.Edges = append(d.Plane.Edges, bpmncore.BPMNEdge{BPMNElement: id})
	}
	return d
}

func compValWithout(ids []string, skip string) []string {
	var out []string
	for _, id := range ids {
		if id != skip {
			out = append(out, id)
		}
	}
	return out
}

func TestValidateDiagramCompleteness_FullyDrawnProcess_ReportsNothing(t *testing.T) {
	t.Parallel()
	proc, shapes, edges := compValDrawnProcess()
	defs := &bpmncore.BPMNDefinitions{Diagrams: []bpmncore.BPMNDiagram{compValDiagram(shapes, edges)}}

	assert.Empty(t, ValidateDiagramCompleteness(proc, defs))
}

func TestValidateDiagramCompleteness_UndrawnElement_ReportsThatElement(t *testing.T) {
	t.Parallel()
	_, shapes, _ := compValDrawnProcess()

	for _, id := range shapes {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			proc, shapes, edges := compValDrawnProcess()
			defs := &bpmncore.BPMNDefinitions{Diagrams: []bpmncore.BPMNDiagram{compValDiagram(compValWithout(shapes, id), edges)}}

			errs := ValidateDiagramCompleteness(proc, defs)

			require.Len(t, errs, 1)
			assert.Equal(t, domain.BPMNErrMissingDiagramShape, errs[0].Code)
			assert.Equal(t, id, errs[0].NodeID)
			assert.Equal(t, domain.SeverityError, errs[0].Severity)
			assert.Contains(t, errs[0].Message, fmt.Sprintf("element %q has no BPMNShape", id))
		})
	}
}

func TestValidateDiagramCompleteness_FlowWithoutAnEdge_IsReportedEvenWhenDrawnAsAShape(t *testing.T) {
	t.Parallel()
	proc, shapes, edges := compValDrawnProcess()
	defs := &bpmncore.BPMNDefinitions{Diagrams: []bpmncore.BPMNDiagram{
		compValDiagram(append(shapes, "F2"), compValWithout(edges, "F2")),
	}}

	errs := ValidateDiagramCompleteness(proc, defs)

	require.Len(t, errs, 1)
	assert.Equal(t, domain.BPMNErrMissingDiagramShape, errs[0].Code)
	assert.Equal(t, "F2", errs[0].NodeID)
	assert.Contains(t, errs[0].Message, `sequence flow "F2" has no BPMNEdge`)
}

func TestValidateDiagramCompleteness_NothingDrawn_ReportsEveryElementAndFlow(t *testing.T) {
	t.Parallel()
	proc, shapes, edges := compValDrawnProcess()
	defs := &bpmncore.BPMNDefinitions{Diagrams: []bpmncore.BPMNDiagram{{}}}

	errs := ValidateDiagramCompleteness(proc, defs)

	var got []string
	for _, e := range errs {
		assert.Equal(t, domain.BPMNErrMissingDiagramShape, e.Code)
		got = append(got, e.NodeID)
	}
	assert.ElementsMatch(t, append(append([]string{}, shapes...), edges...), got)
}

func TestValidateDiagramCompleteness_ShapesAndEdgesAcrossDiagrams_AreCombined(t *testing.T) {
	t.Parallel()
	proc, shapes, edges := compValDrawnProcess()
	half := len(shapes) / 2
	defs := &bpmncore.BPMNDefinitions{Diagrams: []bpmncore.BPMNDiagram{
		compValDiagram(shapes[:half], edges[:1]),
		compValDiagram(shapes[half:], edges[1:]),
	}}

	assert.Empty(t, ValidateDiagramCompleteness(proc, defs))
}

func compValProcessWithTasks(id string, tasks int, calls ...bpmncore.BPMNCallActivity) bpmncore.BPMNProcess {
	p := bpmncore.BPMNProcess{ID: id, CallActivities: calls}
	for i := 0; i < tasks; i++ {
		p.UserTasks = append(p.UserTasks, bpmncore.BPMNUserTask{ID: fmt.Sprintf("%s_t%d", id, i)})
	}
	return p
}

func compValCall(id, processID string) bpmncore.BPMNCallActivity {
	ca := bpmncore.BPMNCallActivity{ID: id}
	if processID != "" {
		ca.ExtensionElements.ZeebeCalledElement = &bpmncore.ZeebeCalledElement{ProcessID: processID}
	}
	return ca
}

func TestValidateCallGraph_UnrelatedRootsAndUnresolvedCalls_AreWalkedIndependently(t *testing.T) {
	t.Parallel()
	full := domain.MaxPlanStages
	noExtension := bpmncore.BPMNCallActivity{ID: "CA_bare"}
	defs := &bpmncore.BPMNDefinitions{Processes: []bpmncore.BPMNProcess{
		compValProcessWithTasks("RootA", full, compValCall("CA_a", "ModA")),
		compValProcessWithTasks("ModA", 0),
		compValProcessWithTasks("RootB", full, compValCall("CA_b", "ModB")),
		compValProcessWithTasks("ModB", 0),
		compValProcessWithTasks("RootC", full, compValCall("CA_empty", ""), noExtension, compValCall("CA_unknown", "Nowhere")),
	}}

	errs := ValidateCallGraph(defs)

	var nodes []string
	for _, e := range errs {
		assert.Equal(t, domain.BPMNErrTaskLimitExceeded, e.Code)
		assert.Equal(t, fmt.Sprintf("process exceeds the %d task limit once its calls are expanded, counting each call", full), e.Message)
		nodes = append(nodes, e.NodeID)
	}
	assert.ElementsMatch(t, []string{"RootA", "RootB"}, nodes)
}

func TestAnchorOn_FillsOnlyEmptyNodeIDs(t *testing.T) {
	t.Parallel()
	errs := []domain.BPMNValidationError{
		{Code: domain.BPMNErrNoStartEvent, Message: "no start"},
		{Code: domain.BPMNErrDanglingNode, NodeID: "T1", Message: "dangling"},
	}

	got := AnchorOn("Module@v3", errs)

	require.Len(t, got, 2)
	assert.Equal(t, "Module@v3", got[0].NodeID)
	assert.Equal(t, "T1", got[1].NodeID)
	assert.Equal(t, "no start", got[0].Message)
	assert.Empty(t, AnchorOn("Module@v3", nil))
}

func TestValidateCallActivity_MessagesNameTheCallAndModule_FallingBackToIDs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		callName   string
		moduleName string
		wantCall   string
		wantModule string
	}{
		{name: "both named", callName: "Pay supplier", moduleName: "Payments", wantCall: "Pay supplier", wantModule: "Payments"},
		{name: "both unnamed", wantCall: "CA1", wantModule: "Mod1"},
		{name: "only the call named", callName: "Pay supplier", wantCall: "Pay supplier", wantModule: "Mod1"},
		{name: "only the module named", moduleName: "Payments", wantCall: "CA1", wantModule: "Payments"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ca := compValCall("CA1", "Mod1")
			ca.Name = tc.callName
			ca.ExtensionElements.IOMapping = &bpmncore.ZeebeIOMapping{Inputs: []bpmncore.ZeebeIOVar{
				{Target: bpmncore.CallInputDeptID, Source: "d"},
				{Target: bpmncore.CallInputDepts, Source: `{"Ghost":"Lane1"}`},
			}}
			module := compValProcessWithTasks("Mod1", 0)
			module.Name = tc.moduleName
			module.StartEvents = []bpmncore.BPMNEvent{{ID: "MS"}}
			caller := compValProcessWithTasks("Main", 0, ca)
			defs := &bpmncore.BPMNDefinitions{Processes: []bpmncore.BPMNProcess{caller, module}}

			errs := ValidateCallActivity(ca, &defs.Processes[0], defs)

			var messages []string
			for _, e := range errs {
				assert.Equal(t, "CA1", e.NodeID)
				messages = append(messages, e.Message)
			}
			assert.Contains(t, messages, fmt.Sprintf("call %q uses a dept_id input, which is not used: a module with one open lane takes the call's lane, and Depts maps any other", tc.wantCall))
			assert.Contains(t, messages, fmt.Sprintf("call %q maps %q in Depts, but module %q has no lane of that name holding work", tc.wantCall, "Ghost", tc.wantModule))
		})
	}
}

func TestValidateWithImplicitStart_BackEdgesComeFromTheFirstStartThenTheImplicitStart(t *testing.T) {
	t.Parallel()

	loopProcess := func(withStart bool) (*bpmncore.BPMNProcess, *bpmncore.Graph) {
		proc := &bpmncore.BPMNProcess{
			UserTasks: []bpmncore.BPMNUserTask{{ID: "A"}, {ID: "B"}},
			SequenceFlows: []bpmncore.BPMNSequenceFlow{
				{ID: "F2", SourceRef: "A", TargetRef: "B"},
				{ID: "F3", SourceRef: "B", TargetRef: "A"},
			},
		}
		if withStart {
			proc.StartEvents = []bpmncore.BPMNEvent{{ID: "S"}}
			proc.SequenceFlows = append(proc.SequenceFlows, bpmncore.BPMNSequenceFlow{ID: "F1", SourceRef: "S", TargetRef: "A"})
		}
		return proc, bpmncore.BuildGraph(proc)
	}

	cases := []struct {
		name          string
		withStart     bool
		implicitStart string
		wantLoopNodes []string
	}{
		{name: "start event alone", withStart: true, wantLoopNodes: []string{"B"}},
		{name: "start event wins over the implicit start", withStart: true, implicitStart: "B", wantLoopNodes: []string{"B"}},
		{name: "implicit start when there is no start event", implicitStart: "B", wantLoopNodes: []string{"A"}},
		{name: "neither start leaves no back-edges", wantLoopNodes: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			proc, g := loopProcess(tc.withStart)

			errs := ValidateWithImplicitStart(proc, g, tc.implicitStart, nil, nil, &bpmncore.BPMNDefinitions{})

			var loopNodes []string
			cycles := 0
			for _, e := range errs {
				if e.Code == domain.BPMNErrUnguardedLoop {
					loopNodes = append(loopNodes, e.NodeID)
				}
				if e.Code == domain.BPMNErrCycleDetected {
					cycles++
					assert.ElementsMatch(t, []string{"A", "B"}, strings.Split(e.NodeID, ","), "cycle node ids %q", e.NodeID)
				}
			}
			assert.Equal(t, tc.wantLoopNodes, loopNodes)
			assert.Equal(t, 1, cycles, "the cycle itself is reported whichever start is used")
		})
	}
}
