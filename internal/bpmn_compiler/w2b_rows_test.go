package bpmn_compiler

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func w2bDefinitions(body string) string {
	return `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="` + nsBPMN + `" xmlns:zeebe="` + nsZeebe + `">
` + body + `
</bpmn:definitions>`
}

func w2bFlowIDs(flows []bpmncore.BPMNSequenceFlow) []string {
	ids := make([]string, 0, len(flows))
	for _, f := range flows {
		ids = append(ids, f.ID)
	}
	return ids
}

func w2bTaskIDs(tasks []bpmncore.BPMNUserTask) []string {
	ids := make([]string, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, task.ID)
	}
	return ids
}

func TestParse_SequenceFlowWithoutBothRefs_IsKeptAtEveryLevel(t *testing.T) {
	halfDrawn := []struct {
		name string
		flow string
	}{
		{"sourceRef attribute missing", `<bpmn:sequenceFlow id="HalfDrawn" targetRef="E"/>`},
		{"targetRef attribute missing", `<bpmn:sequenceFlow id="HalfDrawn" sourceRef="S"/>`},
		{"sourceRef empty", `<bpmn:sequenceFlow id="HalfDrawn" sourceRef="" targetRef="E"/>`},
		{"targetRef empty", `<bpmn:sequenceFlow id="HalfDrawn" sourceRef="S" targetRef=""/>`},
		{"both refs missing", `<bpmn:sequenceFlow id="HalfDrawn"/>`},
	}
	const keptFirst = `<bpmn:sequenceFlow id="KeptFirst" sourceRef="S" targetRef="E"/>`
	const keptLast = `<bpmn:sequenceFlow id="KeptLast" sourceRef="E" targetRef="S"/>`
	levels := []struct {
		name string
		wrap func(flows string) string
		got  func(p bpmncore.BPMNProcess) []bpmncore.BPMNSequenceFlow
	}{
		{
			name: "process",
			wrap: func(flows string) string {
				return `<bpmn:process id="P1"><bpmn:startEvent id="S"/><bpmn:endEvent id="E"/>` + flows + `</bpmn:process>`
			},
			got: func(p bpmncore.BPMNProcess) []bpmncore.BPMNSequenceFlow { return p.SequenceFlows },
		},
		{
			name: "subProcess",
			wrap: func(flows string) string {
				return `<bpmn:process id="P1"><bpmn:subProcess id="SP"><bpmn:startEvent id="S"/><bpmn:endEvent id="E"/>` + flows + `</bpmn:subProcess></bpmn:process>`
			},
			got: func(p bpmncore.BPMNProcess) []bpmncore.BPMNSequenceFlow { return p.SubProcesses[0].SequenceFlows },
		},
	}
	for _, level := range levels {
		for _, tc := range halfDrawn {
			t.Run(level.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				defs, err := parse(context.Background(), w2bDefinitions(level.wrap(keptFirst+tc.flow+keptLast)))
				if err != nil {
					t.Fatalf("parse returned an error, want the half-drawn flow kept for the validator to refuse: %v", err)
				}
				got := w2bFlowIDs(level.got(defs.Processes[0]))
				if want := []string{"KeptFirst", "HalfDrawn", "KeptLast"}; !slices.Equal(got, want) {
					t.Errorf("sequence flows after parse = %v, want %v", got, want)
				}
			})
		}
	}
}

func TestParse_TaskKinds_ArePromotedOrDroppedAtEveryLevel(t *testing.T) {
	const connector = `<bpmn:extensionElements><zeebe:taskDefinition type="connector:storage"/></bpmn:extensionElements>`
	const plain = `<bpmn:extensionElements><zeebe:taskDefinition type="prep"/></bpmn:extensionElements>`
	tasks := func(suffix string) string {
		return `<bpmn:userTask id="User` + suffix + `"/>` +
			`<bpmn:task id="Generic` + suffix + `"/>` +
			`<bpmn:serviceTask id="Connector` + suffix + `">` + connector + `</bpmn:serviceTask>` +
			`<bpmn:serviceTask id="Plain` + suffix + `">` + plain + `</bpmn:serviceTask>` +
			`<bpmn:serviceTask id="Bare` + suffix + `"/>`
	}
	defs, err := parse(context.Background(), w2bDefinitions(
		`<bpmn:process id="P1">`+tasks("Top")+`<bpmn:subProcess id="SP">`+tasks("Sub")+`</bpmn:subProcess></bpmn:process>`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	proc := defs.Processes[0]
	sub := proc.SubProcesses[0]
	levels := []struct {
		name                        string
		user, generic, service      []bpmncore.BPMNUserTask
		wantUserIDs, droppedPlainID []string
	}{
		{"process", proc.UserTasks, proc.GenericTasks, proc.ServiceTasks, []string{"UserTop", "GenericTop", "ConnectorTop"}, []string{"PlainTop", "BareTop"}},
		{"subProcess", sub.UserTasks, sub.GenericTasks, sub.ServiceTasks, []string{"UserSub", "GenericSub", "ConnectorSub"}, []string{"PlainSub", "BareSub"}},
	}
	for _, level := range levels {
		if got := w2bTaskIDs(level.user); !slices.Equal(got, level.wantUserIDs) {
			t.Errorf("%s user tasks = %v, want %v: the generic task and the connector task join them, in that order", level.name, got, level.wantUserIDs)
		}
		if len(level.generic) != 0 {
			t.Errorf("%s generic tasks = %v, want none left after promotion", level.name, w2bTaskIDs(level.generic))
		}
		if len(level.service) != 0 {
			t.Errorf("%s service tasks = %v, want none left: connector tasks are promoted and the rest dropped", level.name, w2bTaskIDs(level.service))
		}
		for _, id := range level.droppedPlainID {
			if slices.Contains(w2bTaskIDs(level.user), id) {
				t.Errorf("%s: non-connector service task %s was promoted into the user tasks", level.name, id)
			}
		}
	}
}

func w2bExpandCollaboration(calledPlan string) *dsl.CompiledCollaboration {
	return &dsl.CompiledCollaboration{
		MainPlan: "Main",
		Plans: []*dsl.CompiledPlan{{
			Name:        "Main",
			Departments: []dsl.DepartmentDef{{ID: "dept-a", Label: "A"}},
			Execution: dsl.ExecutionPlan{Steps: []dsl.ExecutionStep{
				{CallPlan: &dsl.CallPlanStep{NodeID: "Call_1", Plan: calledPlan}},
			}},
		}},
		SchemaVersion: dsl.CurrentSchemaVersion,
	}
}

func TestExpand_FailureOtherThanTheTaskLimit_IsAWrappedPlainError(t *testing.T) {
	duplicate := w2bExpandCollaboration("Main")
	duplicate.Plans = append(duplicate.Plans, &dsl.CompiledPlan{Name: "Main"})
	tests := []struct {
		name       string
		collab     *dsl.CompiledCollaboration
		wantPrefix string
	}{
		{"a call to a plan that is not in the collaboration", w2bExpandCollaboration("Missing"),
			`bpmn compile: expand calls: dsl: `},
		{"two plans with one name", duplicate, `bpmn compile: expand calls: dsl: `},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := expand(tc.collab, "Main")
			if err == nil {
				t.Fatalf("expand returned %+v, want an error", got)
			}
			if got != nil {
				t.Errorf("expand returned a collaboration with its error: %+v", got)
			}
			var failed *domain.ValidationFailedError
			if errors.As(err, &failed) {
				t.Errorf("error = %v, want a plain error, not a validation result the author could fix", err)
			}
			if errors.Is(err, dsl.ErrPlanTooLarge) {
				t.Errorf("error = %v, must not claim the task limit was exceeded", err)
			}
			if !strings.HasPrefix(err.Error(), tc.wantPrefix) {
				t.Errorf("error = %q, want it to start %q", err.Error(), tc.wantPrefix)
			}
			if inner := errors.Unwrap(err); inner == nil || !strings.HasPrefix(inner.Error(), "dsl: ") {
				t.Errorf("unwrapped error = %v, want the expander's own error kept in the chain", inner)
			}
		})
	}
}

type w2bStageType struct{ id, activity string }

func (s w2bStageType) ID() string           { return s.id }
func (s w2bStageType) ActivityName() string { return s.activity }
func (w2bStageType) ValidateProps(string, map[string]string) []domain.BPMNValidationError {
	return nil
}

type w2bElementHandler struct {
	nodeType bpmncore.FlowNodeType
	tag      string
}

func (h w2bElementHandler) NodeType() bpmncore.FlowNodeType { return h.nodeType }
func (w2bElementHandler) Validate(string, *bpmncore.BPMNProcess, *bpmncore.Graph, *bpmncore.BPMNDefinitions, map[string]bpmncore.StageTypeHandler, map[bpmncore.FlowNodeType]bpmncore.ElementHandler) []domain.BPMNValidationError {
	return nil
}
func (w2bElementHandler) Compile(string, *bpmncore.CompileState) error { return nil }

func TestNewCompiler_StageTypeOptions_ReplaceByIDAndTheLastOneWins(t *testing.T) {
	defaults := New()
	if got := len(defaults.stageTypes); got != 3 {
		t.Fatalf("New() has %d stage types, want prep, review and approve", got)
	}

	c := NewCompiler(
		WithStageType(w2bStageType{id: "review", activity: "FirstReview"}),
		WithStageType(w2bStageType{id: "review", activity: "SecondReview"}),
		WithStageType(w2bStageType{id: "extra", activity: "ExtraActivity"}),
	)

	if got := c.stageTypes["review"].ActivityName(); got != "SecondReview" {
		t.Errorf("review activity = %q, want the last option to win", got)
	}
	if got := len(c.stageTypes); got != 4 {
		t.Errorf("stage types = %d, want the default review replaced (3) plus the extra one (4)", got)
	}
	for id, handler := range defaults.stageTypes {
		if id == "review" {
			continue
		}
		if got := c.stageTypes[id].ActivityName(); got != handler.ActivityName() {
			t.Errorf("stage type %q activity = %q, want the untouched default %q", id, got, handler.ActivityName())
		}
	}
	if got := defaults.stageTypes["review"].ActivityName(); got != "ReviewActivity" {
		t.Errorf("an option on one compiler changed another's defaults: review = %q", got)
	}
}

func TestNewCompiler_ElementHandlerOptions_ReplaceByNodeTypeAndTheLastOneWins(t *testing.T) {
	defaults := New()
	c := NewCompiler(
		WithElementHandler(w2bElementHandler{nodeType: bpmncore.NodeTypeUserTask, tag: "first"}),
		WithElementHandler(w2bElementHandler{nodeType: bpmncore.NodeTypeUserTask, tag: "second"}),
	)

	got, ok := c.elements[bpmncore.NodeTypeUserTask].(w2bElementHandler)
	if !ok || got.tag != "second" {
		t.Errorf("user task handler = %#v, want the last option to win", c.elements[bpmncore.NodeTypeUserTask])
	}
	if len(c.elements) != len(defaults.elements) {
		t.Errorf("element handlers = %d, want the default replaced, not added (%d)", len(c.elements), len(defaults.elements))
	}
	if _, ok := defaults.elements[bpmncore.NodeTypeUserTask].(w2bElementHandler); ok {
		t.Error("an option on one compiler replaced the default handler of another")
	}
	if _, ok := c.elements[bpmncore.NodeTypeSendTask].(w2bElementHandler); ok {
		t.Error("the send task handler was replaced although the option named the user task")
	}
}

const w2bHashProcess = `<bpmn:process id="P1" name="Single">
    <bpmn:startEvent id="Start_1"/>
    <bpmn:endEvent id="End_1"/>
    <bpmn:sequenceFlow id="F1" sourceRef="Start_1" targetRef="End_1"/>
  </bpmn:process>`

func TestHash_SingleProcess_SwitchesToTheCombinedHashOnceAReferenceIsAdded(t *testing.T) {
	documents := []struct {
		name string
		body string
	}{
		{"no references", w2bHashProcess},
		{"a message", w2bHashProcess + `<bpmn:message id="M1" name="msg"/>`},
		{"an error", w2bHashProcess + `<bpmn:error id="Err1" errorCode="E_ONE"/>`},
		{"a message flow", `<bpmn:collaboration id="C"><bpmn:participant id="Pa" processRef="P1"/>` +
			`<bpmn:messageFlow id="MF1" sourceRef="Start_1" targetRef="End_1"/></bpmn:collaboration>` + w2bHashProcess},
	}
	c := New()
	seen := map[string]string{}
	var base string
	for i, tc := range documents {
		xmlData := w2bDefinitions(tc.body)
		got, err := c.Hash(context.Background(), xmlData)
		if err != nil {
			t.Fatalf("%s: Hash: %v", tc.name, err)
		}
		defs, err := parse(context.Background(), xmlData)
		if err != nil {
			t.Fatalf("%s: parse: %v", tc.name, err)
		}
		processHash, err := canonicalHash(&defs.Processes[0])
		if err != nil {
			t.Fatalf("%s: canonicalHash: %v", tc.name, err)
		}
		if i == 0 {
			base = got
			if got != processHash {
				t.Errorf("%s: hash = %s, want the process's own canonical hash %s", tc.name, got, processHash)
			}
		} else {
			if processHash != base {
				t.Fatalf("%s: the process itself hashes differently (%s vs %s); the fixture must only add a reference", tc.name, processHash, base)
			}
			if got == processHash {
				t.Errorf("%s: hash equals the bare process hash, want the combined hash once a reference exists", tc.name)
			}
		}
		if other, dup := seen[got]; dup {
			t.Errorf("%s hashes like %s", tc.name, other)
		}
		seen[got] = tc.name
	}
}
