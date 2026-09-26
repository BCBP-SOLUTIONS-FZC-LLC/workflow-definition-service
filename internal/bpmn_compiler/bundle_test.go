package bpmn_compiler

import (
	"strings"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

const bundleMainXML = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="D1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="MainProc" name="Main">
    <bpmn:startEvent id="Start1"/>
    <bpmn:callActivity id="CA1"><bpmn:extensionElements><zeebe:calledElement processId="ModuleProc" bindingType="versionTag" versionTag="2"/></bpmn:extensionElements></bpmn:callActivity>
    <bpmn:endEvent id="End1"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="Diag1">
    <bpmndi:BPMNPlane bpmnElement="MainProc"/>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`

const bundleModuleXML = `<?xml version="1.0" encoding="UTF-8"?>
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

func moduleV2() domain.ResolvedModule {
	return domain.ResolvedModule{ProcessID: "ModuleProc", VersionNumber: 2, PlanName: "ModuleProc@v2", BPMNXML: bundleModuleXML}
}

func TestBundleModules_NoModules_ReturnsMainUnchanged(t *testing.T) {
	out, err := bundleModules(bundleMainXML, nil)
	if err != nil || out != bundleMainXML {
		t.Fatalf("bundleModules(nil) = %q, %v; want the main XML unchanged", out, err)
	}
}

func TestBundleModules_RenamesTheModuleAndPointsTheCallAtIt(t *testing.T) {
	out, err := bundleModules(bundleMainXML, []domain.ResolvedModule{moduleV2()})
	if err != nil {
		t.Fatalf("bundleModules: %v", err)
	}
	if !strings.Contains(out, `id="ModuleProc@v2"`) {
		t.Error("the module process was not bundled under its plan name")
	}
	if !strings.Contains(out, `processId="ModuleProc@v2"`) || strings.Contains(out, `versionTag="2"`) {
		t.Error("the call still names the library module rather than its bundled plan")
	}
	if strings.Contains(out, "Diag2") {
		t.Error("the module's diagram was bundled")
	}
}

func TestBundleModules_OnePlanReachedTwiceIsAddedOnce(t *testing.T) {
	latest := moduleV2()
	latest.VersionNumber = 0
	out, err := bundleModules(bundleMainXML, []domain.ResolvedModule{moduleV2(), latest})
	if err != nil {
		t.Fatalf("bundleModules: %v", err)
	}
	if n := strings.Count(out, `id="ModuleProc@v2"`); n != 1 {
		t.Errorf("ModuleProc@v2 bundled %d times, want once", n)
	}
}

func TestBundleModules_InvalidMainOrModule(t *testing.T) {
	if _, err := bundleModules("<not xml", []domain.ResolvedModule{moduleV2()}); err == nil {
		t.Error("an unparseable main BPMN bundled without an error")
	}
	bad := moduleV2()
	bad.BPMNXML = `<!DOCTYPE x [<!ENTITY a "b">]><bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"/>`
	if _, err := bundleModules(bundleMainXML, []domain.ResolvedModule{bad}); err == nil {
		t.Error("a module with a forbidden DOCTYPE bundled without an error")
	}
}
