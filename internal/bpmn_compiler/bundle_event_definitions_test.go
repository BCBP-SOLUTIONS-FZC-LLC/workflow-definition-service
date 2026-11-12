package bpmn_compiler

import (
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

const bundleEventModuleXML = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="D3" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:message id="MsgIn" name="order-in"/>
  <bpmn:message id="MsgOut" name="order-out"/>
  <bpmn:error id="ErrFail" name="fail" errorCode="E_FAIL"/>
  <bpmn:process id="ModuleProc" name="Module">
    <bpmn:startEvent id="ModStart"><bpmn:messageEventDefinition messageRef="MsgIn"/></bpmn:startEvent>
    <bpmn:subProcess id="Sub1">
      <bpmn:startEvent id="SubStart"><bpmn:messageEventDefinition messageRef="MsgOut"/></bpmn:startEvent>
      <bpmn:endEvent id="SubEnd"><bpmn:errorEventDefinition errorRef="ErrFail"/></bpmn:endEvent>
    </bpmn:subProcess>
    <bpmn:endEvent id="ModEnd"><bpmn:errorEventDefinition errorRef="ErrFail"/></bpmn:endEvent>
  </bpmn:process>
</bpmn:definitions>`

func TestBundleModules_KeepsAndPrefixesEventDefinitionsOfStartAndEndEvents(t *testing.T) {
	tests := []struct {
		name   string
		pick   func(p *bpmncore.BPMNProcess) (messageRef, errorRef string, present bool)
		wantM  string
		wantE  string
		wantIn string
	}{
		{
			name: "module start event keeps its message definition",
			pick: func(p *bpmncore.BPMNProcess) (string, string, bool) {
				ev := p.StartEvents[0]
				if ev.Message == nil {
					return "", "", false
				}
				return ev.Message.MessageRef, "", true
			},
			wantM: "ModuleProc@v2/MsgIn",
		},
		{
			name: "module end event keeps its error definition",
			pick: func(p *bpmncore.BPMNProcess) (string, string, bool) {
				ev := p.EndEvents[0]
				if ev.Error == nil {
					return "", "", false
				}
				return "", ev.Error.ErrorRef, true
			},
			wantE: "ModuleProc@v2/ErrFail",
		},
		{
			name: "sub-process start event keeps its message definition",
			pick: func(p *bpmncore.BPMNProcess) (string, string, bool) {
				ev := p.SubProcesses[0].StartEvents[0]
				if ev.Message == nil {
					return "", "", false
				}
				return ev.Message.MessageRef, "", true
			},
			wantM: "ModuleProc@v2/MsgOut",
		},
		{
			name: "sub-process end event keeps its error definition",
			pick: func(p *bpmncore.BPMNProcess) (string, string, bool) {
				ev := p.SubProcesses[0].EndEvents[0]
				if ev.Error == nil {
					return "", "", false
				}
				return "", ev.Error.ErrorRef, true
			},
			wantE: "ModuleProc@v2/ErrFail",
		},
	}
	mod := domain.ResolvedModule{ProcessID: "ModuleProc", VersionNumber: 2, PlanName: "ModuleProc@v2", BPMNXML: bundleEventModuleXML}
	out, err := bundleModules(bundleMainXML, []domain.ResolvedModule{mod})
	if err != nil {
		t.Fatalf("bundleModules: %v", err)
	}
	defs, err := unmarshal(out)
	if err != nil {
		t.Fatalf("unmarshal bundled BPMN: %v", err)
	}
	var proc *bpmncore.BPMNProcess
	for i := range defs.Processes {
		if defs.Processes[i].ID == "ModuleProc@v2" {
			proc = &defs.Processes[i]
		}
	}
	if proc == nil {
		t.Fatal("module process ModuleProc@v2 missing from the bundle")
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, e, present := tc.pick(proc)
			if !present {
				t.Fatal("the event definition was dropped by the bundle")
			}
			if m != tc.wantM || e != tc.wantE {
				t.Errorf("refs = (message %q, error %q), want (%q, %q)", m, e, tc.wantM, tc.wantE)
			}
		})
	}
	ids := map[string]bool{}
	for _, m := range defs.Messages {
		ids[m.ID] = true
	}
	for _, e := range defs.Errors {
		ids[e.ID] = true
	}
	for _, want := range []string{"ModuleProc@v2/MsgIn", "ModuleProc@v2/MsgOut", "ModuleProc@v2/ErrFail"} {
		if !ids[want] {
			t.Errorf("definition %q missing from the bundle, have %v", want, ids)
		}
	}
}
