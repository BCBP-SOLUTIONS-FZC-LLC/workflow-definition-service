package bpmn_compiler

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestSecurityScan_DocTypeRejected(t *testing.T) {
	xmlData := `<?xml version="1.0"?><!DOCTYPE foo [<!ENTITY x "y">]><root/>`
	if err := securityScan(xmlData); err == nil {
		t.Error("securityScan() should reject DOCTYPE declarations")
	}
}

func TestSecurityScan_EntityRejected(t *testing.T) {
	xmlData := `<?xml version="1.0"?><!ENTITY xxe SYSTEM "file:///etc/passwd"><root/>`
	if err := securityScan(xmlData); err == nil {
		t.Error("securityScan() should reject ENTITY declarations")
	}
}

func TestSecurityScan_LowercaseVariantsRejected(t *testing.T) {
	xmlData := `<?xml version="1.0"?><!doctype foo [<!entity x "y">]><root/>`
	if err := securityScan(xmlData); err == nil {
		t.Error("securityScan() should reject lowercase doctype/entity")
	}
}

func TestSecurityScan_Clean(t *testing.T) {
	xmlData := `<?xml version="1.0"?><root><!-- comment --><child/></root>`
	if err := securityScan(xmlData); err != nil {
		t.Errorf("securityScan() returned unexpected error for clean XML: %v", err)
	}
}

func TestCountTokens_LimitExceeded(t *testing.T) {
	var b strings.Builder
	b.WriteString("<root>")
	for i := 0; i < maxTokens/2+2; i++ {
		b.WriteString("<a/>")
	}
	b.WriteString("</root>")

	err := countTokens(context.Background(), b.String())
	if err == nil {
		t.Error("countTokens() should return error when token limit is exceeded")
	}
}

func TestCountTokens_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := countTokens(ctx, "<root><a/><b/><c/></root>")
	if err == nil {
		t.Error("countTokens() should return error when context is already cancelled")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestCountTokens_ValidXML(t *testing.T) {
	err := countTokens(context.Background(), "<root><child/></root>")
	if err != nil {
		t.Errorf("countTokens() unexpected error for valid XML: %v", err)
	}
}

func TestParse_PropagatesForbiddenDoctypeFromSecurityScan(t *testing.T) {
	xmlData := `<?xml version="1.0"?><!DOCTYPE foo [<!ENTITY x "y">]><bpmn:definitions xmlns:bpmn="` + nsBPMN + `" xmlns:zeebe="` + nsZeebe + `"/>`
	_, err := parse(context.Background(), xmlData)
	if !errors.Is(err, errForbiddenXML) {
		t.Errorf("parse() should propagate errForbiddenXML for a DOCTYPE document; got %v", err)
	}
}

func TestParse_PropagatesTokenLimitFromCountTokens(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><bpmn:definitions xmlns:bpmn="` + nsBPMN + `" xmlns:zeebe="` + nsZeebe + `">`)
	for i := 0; i < maxTokens/2+2; i++ {
		b.WriteString("<a/>")
	}
	b.WriteString("</bpmn:definitions>")

	_, err := parse(context.Background(), b.String())
	if !errors.Is(err, errForbiddenXML) {
		t.Errorf("parse() should propagate errForbiddenXML for a token-limit-exceeded document; got %v", err)
	}
}

func TestParse_WrongNamespace(t *testing.T) {
	xmlData := `<?xml version="1.0"?><definitions xmlns="http://wrong.namespace/v1" id="D1"/>`
	_, err := parse(context.Background(), xmlData)
	if !errors.Is(err, errMissingNamespace) {
		t.Errorf("parse() should return errMissingNamespace for a non-BPMN root; got %v", err)
	}
}

func TestParse_MissingZeebeNamespace(t *testing.T) {
	xmlData := `<?xml version="1.0"?><bpmn:definitions xmlns:bpmn="` + nsBPMN + `" id="D1"/>`
	_, err := parse(context.Background(), xmlData)
	if !errors.Is(err, errMissingNamespace) {
		t.Errorf("parse() should return errMissingNamespace when zeebe ns is undeclared; got %v", err)
	}
}

func TestParse_MalformedXMLPastNamespaceCheck(t *testing.T) {
	xmlData := `<?xml version="1.0"?><bpmn:definitions xmlns:bpmn="` + nsBPMN +
		`" xmlns:zeebe="` + nsZeebe + `"><unclosed>`
	_, err := parse(context.Background(), xmlData)
	if err == nil {
		t.Error("parse() should return error for malformed XML")
	}
	if errors.Is(err, errMissingNamespace) {
		t.Errorf("malformed XML should not be reported as a namespace error; got %v", err)
	}
}

func TestParse_ValidMinimal(t *testing.T) {
	xmlData := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<bpmn:definitions xmlns:bpmn="` + nsBPMN + `" xmlns:zeebe="` + nsZeebe + `" id="D1"/>`
	defs, err := parse(context.Background(), xmlData)
	if err != nil {
		t.Fatalf("parse() unexpected error: %v", err)
	}
	if defs == nil {
		t.Error("parse() returned nil definitions for valid XML")
	}
}

func TestScanRejected_ServiceTaskRejectedSubProcessAllowed(t *testing.T) {
	xmlData := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="` + nsBPMN + `" xmlns:zeebe="` + nsZeebe + `">
  <bpmn:process id="P1">
    <bpmn:serviceTask id="SVC_1" name="Call API"/>
    <bpmn:subProcess id="SUB_1"/>
    <bpmn:userTask id="T1"/>
  </bpmn:process>
</bpmn:definitions>`
	errs := scanRejected(xmlData)
	if !hasCodeIn(errs, domain.BPMNErrRejectedElement) {
		t.Fatalf("expected REJECTED_ELEMENT; got %v", errCodesOf(errs))
	}
	if len(errs) != 1 {
		t.Errorf("expected 1 rejected element (serviceTask); got %d: %v", len(errs), errs)
	}
	if errs[0].NodeID != "SVC_1" {
		t.Errorf("expected node id SVC_1; got %q", errs[0].NodeID)
	}
}

func TestScanRejected_InclusiveGatewayIsUnsupported(t *testing.T) {
	xmlData := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="` + nsBPMN + `" xmlns:zeebe="` + nsZeebe + `">
  <bpmn:process id="P1">
    <bpmn:inclusiveGateway id="IG_1" name="OR Split"/>
    <bpmn:userTask id="T1"/>
  </bpmn:process>
</bpmn:definitions>`
	errs := scanRejected(xmlData)
	if !hasCodeIn(errs, domain.BPMNErrUnsupportedElement) {
		t.Fatalf("inclusiveGateway must produce UNSUPPORTED_ELEMENT; got %v", errCodesOf(errs))
	}
}

func TestScanRejected_ElementWithoutIDFallsBackToLocalName(t *testing.T) {
	xmlData := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="` + nsBPMN + `" xmlns:zeebe="` + nsZeebe + `">
  <bpmn:process id="P1">
    <bpmn:serviceTask name="Call API"/>
  </bpmn:process>
</bpmn:definitions>`
	errs := scanRejected(xmlData)
	if !hasCodeIn(errs, domain.BPMNErrRejectedElement) {
		t.Fatalf("expected REJECTED_ELEMENT; got %v", errCodesOf(errs))
	}
	if errs[0].NodeID != "serviceTask" {
		t.Errorf("expected fallback NodeID = local name %q; got %q", "serviceTask", errs[0].NodeID)
	}
}

func TestParse_SubProcessGenericTaskPromotion(t *testing.T) {
	xmlData := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="` + nsBPMN + `" xmlns:zeebe="` + nsZeebe + `">
  <bpmn:process id="P1">
    <bpmn:subProcess id="SP1">
      <bpmn:task id="GT1" name="Generic"/>
      <bpmn:sequenceFlow id="F1" sourceRef="" targetRef=""/>
    </bpmn:subProcess>
  </bpmn:process>
</bpmn:definitions>`
	defs, err := parse(context.Background(), xmlData)
	if err != nil {
		t.Fatalf("parse() unexpected error: %v", err)
	}
	if len(defs.Processes) != 1 || len(defs.Processes[0].SubProcesses) != 1 {
		t.Fatalf("expected 1 process with 1 subprocess; got %+v", defs.Processes)
	}
	sp := defs.Processes[0].SubProcesses[0]
	if len(sp.GenericTasks) != 0 {
		t.Errorf("GenericTasks should be cleared after promotion; got %d", len(sp.GenericTasks))
	}
	found := false
	for _, ut := range sp.UserTasks {
		if ut.ID == "GT1" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected generic task GT1 promoted into subProcess.UserTasks; got %+v", sp.UserTasks)
	}
	if len(sp.SequenceFlows) != 0 {
		t.Errorf("empty-ref sequence flow should be stripped from subProcess; got %+v", sp.SequenceFlows)
	}
}

func TestScanRejected_AllowlistedElementsAndDiagramNamespaceClean(t *testing.T) {
	xmlData := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="` + nsBPMN + `" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI">
  <bpmn:process id="P1">
    <bpmn:startEvent id="S1"/>
    <bpmn:userTask id="T1"/>
    <bpmn:endEvent id="E1"/>
    <bpmn:sequenceFlow id="F1" sourceRef="S1" targetRef="T1"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="Diagram_1"/>
</bpmn:definitions>`
	if errs := scanRejected(xmlData); len(errs) != 0 {
		t.Errorf("clean document should have no rejected elements; got %v", errCodesOf(errs))
	}
}

func TestScanRejected_EventBasedGatewayIsUnsupported(t *testing.T) {
	xmlData := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="` + nsBPMN + `" xmlns:zeebe="` + nsZeebe + `">
  <bpmn:process id="P1">
    <bpmn:eventBasedGateway id="EBG_1"/>
    <bpmn:userTask id="T1"/>
  </bpmn:process>
</bpmn:definitions>`
	errs := scanRejected(xmlData)
	if !hasCodeIn(errs, domain.BPMNErrUnsupportedElement) {
		t.Fatalf("eventBasedGateway must produce UNSUPPORTED_ELEMENT; got %v", errCodesOf(errs))
	}
}

func TestScanRejected_IntermediateCatchEventIsUnsupported(t *testing.T) {
	xmlData := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="` + nsBPMN + `" xmlns:zeebe="` + nsZeebe + `">
  <bpmn:process id="P1">
    <bpmn:intermediateCatchEvent id="ICE_1"/>
    <bpmn:userTask id="T1"/>
  </bpmn:process>
</bpmn:definitions>`
	errs := scanRejected(xmlData)
	if !hasCodeIn(errs, domain.BPMNErrUnsupportedElement) {
		t.Fatalf("intermediateCatchEvent must produce UNSUPPORTED_ELEMENT; got %v", errCodesOf(errs))
	}
}

func TestScanRejected_SignalEventDefinitionRejected(t *testing.T) {
	xmlData := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="` + nsBPMN + `" xmlns:zeebe="` + nsZeebe + `">
  <bpmn:process id="P1">
    <bpmn:boundaryEvent id="BE_1" attachedToRef="T1">
      <bpmn:signalEventDefinition id="SIG_1"/>
    </bpmn:boundaryEvent>
    <bpmn:userTask id="T1"/>
  </bpmn:process>
</bpmn:definitions>`
	errs := scanRejected(xmlData)
	if !hasCodeIn(errs, domain.BPMNErrRejectedElement) {
		t.Fatalf("signalEventDefinition must produce REJECTED_ELEMENT; got %v", errCodesOf(errs))
	}
}

func TestScanRejected_UnrecognizedElementRejected(t *testing.T) {
	xmlData := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="` + nsBPMN + `" xmlns:zeebe="` + nsZeebe + `">
  <bpmn:process id="P1">
    <bpmn:fooBarBaz id="X1"/>
    <bpmn:userTask id="T1"/>
  </bpmn:process>
</bpmn:definitions>`
	errs := scanRejected(xmlData)
	if !hasCodeIn(errs, domain.BPMNErrRejectedElement) {
		t.Fatalf("unrecognized element must produce REJECTED_ELEMENT; got %v", errCodesOf(errs))
	}
	if errs[0].NodeID != "X1" {
		t.Errorf("expected node id X1; got %q", errs[0].NodeID)
	}
}

func TestScanRejected_ElementsAddedToAllowlistAreNotRejected(t *testing.T) {
	xmlData := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="` + nsBPMN + `" xmlns:zeebe="` + nsZeebe + `">
  <bpmn:process id="P1">
    <bpmn:startEvent id="S1"/>
    <bpmn:task id="TASK_1"/>
    <bpmn:dataStoreReference id="DS_1"/>
    <bpmn:boundaryEvent id="BE_1" attachedToRef="TASK_1">
      <bpmn:timerEventDefinition id="TIMER_1">
        <bpmn:timeDuration>PT24H</bpmn:timeDuration>
      </bpmn:timerEventDefinition>
    </bpmn:boundaryEvent>
    <bpmn:boundaryEvent id="BE_2" attachedToRef="TASK_1">
      <bpmn:errorEventDefinition id="ERR_1"/>
    </bpmn:boundaryEvent>
    <bpmn:boundaryEvent id="BE_3" attachedToRef="TASK_1">
      <bpmn:messageEventDefinition id="MSG_1"/>
    </bpmn:boundaryEvent>
    <bpmn:endEvent id="E1"/>
    <bpmn:sequenceFlow id="F1" sourceRef="S1" targetRef="TASK_1"/>
  </bpmn:process>
</bpmn:definitions>`
	if errs := scanRejected(xmlData); len(errs) != 0 {
		t.Errorf("previously-missing-but-supported elements must not be rejected; got %v", errCodesOf(errs))
	}
}
