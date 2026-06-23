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

func TestSecurityScan_LowercaseNotRejected(t *testing.T) {
	// Lowercase variants must not bypass the check (ToUpper is applied).
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
	// Build an XML doc with > maxTokens tokens.
	// Each <a/> produces 2 tokens (StartElement + EndElement).
	// maxTokens/2 + 2 elements → (maxTokens/2+2)*2 + 2 (root wrapper) tokens > maxTokens+1.
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

func TestParse_WrongNamespace(t *testing.T) {
	xmlData := `<?xml version="1.0"?><definitions xmlns="http://wrong.namespace/v1" id="D1"/>`
	_, err := parse(context.Background(), xmlData)
	if !errors.Is(err, errMissingNamespace) {
		t.Errorf("parse() should return errMissingNamespace for a non-BPMN root; got %v", err)
	}
}

func TestParse_MissingZeebeNamespace(t *testing.T) {
	// Valid BPMN root namespace, but the required Zeebe namespace is absent.
	xmlData := `<?xml version="1.0"?><bpmn:definitions xmlns:bpmn="` + nsBPMN + `" id="D1"/>`
	_, err := parse(context.Background(), xmlData)
	if !errors.Is(err, errMissingNamespace) {
		t.Errorf("parse() should return errMissingNamespace when zeebe ns is undeclared; got %v", err)
	}
}

func TestParse_MalformedXML(t *testing.T) {
	// Declares both required namespaces so parsing reaches (and fails at) unmarshal
	// rather than short-circuiting on a namespace check.
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

func TestScanRejected_RejectedElement(t *testing.T) {
	// serviceTask is Tier 3 (REJECTED_ELEMENT); subProcess is now Tier 1 (no error).
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
	// Only serviceTask is rejected; subProcess is Tier 1.
	if len(errs) != 1 {
		t.Errorf("expected 1 rejected element (serviceTask); got %d: %v", len(errs), errs)
	}
	if errs[0].NodeID != "SVC_1" {
		t.Errorf("expected node id SVC_1; got %q", errs[0].NodeID)
	}
}

func TestScanRejected_InclusiveGatewayIsSupported(t *testing.T) {
	// inclusiveGateway was promoted from Tier 2 (UNSUPPORTED_ELEMENT) to Tier 1 (supported).
	xmlData := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="` + nsBPMN + `" xmlns:zeebe="` + nsZeebe + `">
  <bpmn:process id="P1">
    <bpmn:inclusiveGateway id="IG_1" name="OR Split"/>
    <bpmn:userTask id="T1"/>
  </bpmn:process>
</bpmn:definitions>`
	errs := scanRejected(xmlData)
	if hasCodeIn(errs, domain.BPMNErrUnsupportedElement) {
		t.Fatalf("inclusiveGateway must not produce UNSUPPORTED_ELEMENT; got %v", errCodesOf(errs))
	}
}

func TestScanRejected_CleanDocument(t *testing.T) {
	// Allowlisted BPMN elements plus a diagram-interchange element (different
	// namespace) must not trip the nsBPMN-scoped reject scan.
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
