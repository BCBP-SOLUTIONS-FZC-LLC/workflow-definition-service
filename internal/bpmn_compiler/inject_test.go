package bpmn_compiler

import (
	"strings"
	"testing"
)

const injectMainXML = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  id="D1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="MainProc" name="Main">
    <bpmn:startEvent id="Start1"/>
    <bpmn:endEvent id="End1"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="Diag1">
    <bpmndi:BPMNPlane bpmnElement="MainProc"/>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`

const injectModuleXML = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="D2" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="ModuleProc" name="Module">
    <bpmn:startEvent id="Start2"/>
    <bpmn:endEvent id="End2"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="Diag2">
    <bpmndi:BPMNPlane bpmnElement="ModuleProc"/>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`

func TestInjectTemplates_Empty(t *testing.T) {
	out, err := InjectTemplates(injectMainXML, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != injectMainXML {
		t.Error("expected mainXML returned unchanged when moduleXMLs is empty")
	}
}

func TestInjectTemplates_EmptySlice(t *testing.T) {
	out, err := InjectTemplates(injectMainXML, []string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != injectMainXML {
		t.Error("expected mainXML returned unchanged when moduleXMLs is empty slice")
	}
}

func TestInjectTemplates_OneModule(t *testing.T) {
	out, err := InjectTemplates(injectMainXML, []string{injectModuleXML})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "ModuleProc") {
		t.Error("expected module process ID in bundled output")
	}
	if !strings.Contains(out, "MainProc") {
		t.Error("expected main process ID in bundled output")
	}
	// Module diagram must NOT appear in output.
	if strings.Contains(out, "Diag2") {
		t.Error("module diagram should not be present in bundled output")
	}
}

func TestInjectTemplates_InvalidMain(t *testing.T) {
	_, err := InjectTemplates("not valid xml <<<", []string{injectModuleXML})
	if err == nil {
		t.Error("expected error for malformed main XML")
	}
}

func TestInjectTemplates_InvalidModule(t *testing.T) {
	_, err := InjectTemplates(injectMainXML, []string{"not valid xml <<<"})
	if err == nil {
		t.Error("expected error for malformed module XML")
	}
}

func TestCompiler_Bundle_Passthrough(t *testing.T) {
	c := New()
	out, err := c.Bundle(injectMainXML, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != injectMainXML {
		t.Error("expected mainXML unchanged with nil modules")
	}
}

func TestCompiler_Bundle_InjectsModule(t *testing.T) {
	c := New()
	out, err := c.Bundle(injectMainXML, []string{injectModuleXML})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "ModuleProc") {
		t.Error("expected ModuleProc in bundled output")
	}
}
