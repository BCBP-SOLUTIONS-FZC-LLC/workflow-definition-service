package bpmn_compiler

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

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
    <bpmn:laneSet id="LS"><bpmn:lane id="L1" name="ops"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="bd056344-b10d-5b31-8903-9f98dea26c6b"/></zeebe:properties></bpmn:extensionElements>
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
    <bpmn:laneSet id="LS"><bpmn:lane id="L1" name="ops"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="bd056344-b10d-5b31-8903-9f98dea26c6b"/></zeebe:properties></bpmn:extensionElements>
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

// TestCompile_ParallelSplitWithoutAJoin_IsUnmatchedGateway pins that a
// parallel split whose branches each end at their own end event is refused by
// validation: only an exclusive split may do without a join.
func TestCompile_ParallelSplitWithoutAJoin_IsUnmatchedGateway(t *testing.T) {
	bpmnXML := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="` + nsBPMN + `"
  xmlns:bpmndi="` + nsBPMNDI + `"
  xmlns:zeebe="` + nsZeebe + `"
  id="D1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="P1" name="Test" isExecutable="true">
    <bpmn:laneSet id="LS"><bpmn:lane id="L1" name="ops"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="bd056344-b10d-5b31-8903-9f98dea26c6b"/></zeebe:properties></bpmn:extensionElements>
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
	if valErr != nil || !hasCodeIn(valErrs, domain.BPMNErrUnmatchedGateway) {
		t.Fatalf("Validate = %v, %v; want UNMATCHED_GATEWAY", valErr, errCodesOf(valErrs))
	}

	_, err := c.Compile(context.Background(), bpmnXML)
	var vfe *domain.ValidationFailedError
	if !errors.As(err, &vfe) || !hasCodeIn(vfe.Errors, domain.BPMNErrUnmatchedGateway) {
		t.Fatalf("Compile = %v; want a ValidationFailedError with UNMATCHED_GATEWAY", err)
	}
}

// ─── Element handler overrides ───────────────────────────────────────────────

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

// ─── CompileForPublish and Hash ──────────────────────────────────────────────

func TestCompileForPublish_InvalidBPMN_ReturnsError(t *testing.T) {
	if _, err := New().CompileForPublish(context.Background(), `<<not valid xml>>`); err == nil {
		t.Fatal("expected an error for malformed BPMN")
	}
}

func TestHash_NoProcess_ReturnsError(t *testing.T) {
	bpmnXML := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="` + nsBPMN + `" xmlns:zeebe="` + nsZeebe + `">
</bpmn:definitions>`
	c := New()
	_, err := c.Hash(context.Background(), bpmnXML)
	if err == nil || !strings.Contains(err.Error(), "no process") {
		t.Fatalf("expected the no-process error, got %v", err)
	}
}
