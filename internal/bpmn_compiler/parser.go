package bpmn_compiler

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/validator"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

const (
	nsBPMN   = "http://www.omg.org/spec/BPMN/20100524/MODEL"
	nsZeebe  = "http://camunda.org/schema/zeebe/1.0"
	nsBPMNDI = "http://www.omg.org/spec/BPMN/20100524/DI"
)

// maxTokens bounds the XML token stream as defence-in-depth against pathological
// documents. Scaled to the 10 MB body cap; forbidden DOCTYPE/entity declarations
// are already rejected outright (securityScan), so entity-expansion bombs cannot occur.
const maxTokens = 1_000_000

// errMissingNamespace marks a document that does not declare the required BPMN
// or Zeebe namespaces (§4.1.1). The compiler translates it into a structured
// MISSING_NAMESPACE validation error rather than a generic parse failure.
var errMissingNamespace = errors.New("missing required BPMN or Zeebe namespace")

// errForbiddenXML marks a forbidden-construct parse failure (DOCTYPE/entity
// declaration or the token-limit XML-bomb guard). The client response is the
// same generic 400 INVALID_BPMN_XML as any unparseable document — detection is
// not disclosed — but the handler emits an internal security-alert log when it
// sees this sentinel in the chain.
var errForbiddenXML = errors.New("forbidden XML construct")

// rejectedBPMNElems is the Tier 3 denylist: BPMN elements that will never be
// supported. A denylist avoids rejecting benign standard elements like
// <bpmn:incoming> / <bpmn:outgoing>. encoding/xml silently drops unknown
// elements during unmarshal, so the raw token scan is the only place to catch them.
var rejectedBPMNElems = map[string]struct{}{
	// Tasks
	"serviceTask": {}, "scriptTask": {}, "businessRuleTask": {},
	"transaction": {}, "adHocSubProcess": {},
	// Gateways
	"complexGateway": {},
	// Event definitions (any boundary/intermediate/end with these definitions)
	"terminateEventDefinition": {}, "compensateEventDefinition": {},
	"cancelEventDefinition": {}, "escalationEventDefinition": {},
	"conditionalEventDefinition": {}, "linkEventDefinition": {},
	// Loop markers
	"standardLoopCharacteristics": {},
	// Collaboration / data (conversation and choreography are not multi-pool collaboration)
	"conversation": {}, "choreography": {},
	"dataObject": {}, "dataStore": {}, "dataObjectReference": {}, "dataStoreReference": {},
	// callActivity (Tier 2 callActivity-as-subprocess is tracked separately, not via callActivity element)
	"callActivity": {},
}

var unsupportedBPMNElems = map[string]struct{}{}

func parse(ctx context.Context, xmlData string) (*bpmncore.BPMNDefinitions, error) {
	if err := securityScan(xmlData); err != nil {
		return nil, err
	}
	if err := countTokens(ctx, xmlData); err != nil {
		return nil, err
	}
	// §4.1.1 requires both namespaces declared on the root. Checking the raw text
	// before unmarshal is reliable: xml.Decode rejects a mismatched-namespace root
	// with a generic decode error, which would otherwise mask the real cause.
	if !strings.Contains(xmlData, nsBPMN) {
		return nil, fmt.Errorf("%w: the BPMN namespace (%s) is not declared", errMissingNamespace, nsBPMN)
	}
	if !strings.Contains(xmlData, nsZeebe) {
		return nil, fmt.Errorf("%w: the Zeebe namespace (%s) is not declared", errMissingNamespace, nsZeebe)
	}
	defs, err := unmarshal(xmlData)
	if err != nil {
		return nil, err
	}
	return defs, nil
}

// scanRejected scans the raw XML token stream for Tier 3 (rejected) and Tier 2
// (unsupported) BPMN elements. encoding/xml silently drops unknown elements
// during unmarshal, so the scan must run on the raw text before unmarshal.
func scanRejected(xmlData string) []domain.BPMNValidationError {
	dec := xml.NewDecoder(strings.NewReader(xmlData))
	dec.Strict = true
	var errs []domain.BPMNValidationError
	for {
		tok, err := dec.Token()
		if err != nil {
			return errs // EOF or parse error already surfaced elsewhere
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Space != nsBPMN {
			continue
		}
		if _, rejected := rejectedBPMNElems[se.Name.Local]; rejected {
			errs = validator.AppendErr(errs, domain.BPMNErrRejectedElement, elementID(se),
				fmt.Sprintf("BPMN element <%s> is not supported (§4.1.2 Tier 3)", se.Name.Local))
			continue
		}
		if _, unsupported := unsupportedBPMNElems[se.Name.Local]; unsupported {
			errs = validator.AppendErr(errs, domain.BPMNErrUnsupportedElement, elementID(se),
				fmt.Sprintf("BPMN element <%s> is planned but not yet implemented (§4.1.2 Tier 2)", se.Name.Local))
		}
	}
}

// elementID returns the element's id attribute, or its local name when absent,
// so a REJECTED_ELEMENT error always points at something identifiable.
func elementID(se xml.StartElement) string {
	for _, a := range se.Attr {
		if a.Name.Local == "id" {
			return a.Value
		}
	}
	return se.Name.Local
}

// 4 KB is sufficient: DOCTYPE must appear before the root element.
// Scanning the whole document would be wasteful and <!ENTITY never appears mid-stream.
// XML comments (<!--) start with <! but are legitimate — only match complete keywords.
func securityScan(xmlData string) error {
	head := xmlData
	if len(head) > 4096 {
		head = head[:4096]
	}
	upper := strings.ToUpper(head)
	if strings.Contains(upper, "<!DOCTYPE") || strings.Contains(upper, "<!ENTITY") {
		return fmt.Errorf("%w: DOCTYPE or entity declaration detected", errForbiddenXML)
	}
	return nil
}

// Second line of defence against entity expansion attacks that omit explicit
// declarations but rely on deeply nested or repeated constructs.
func countTokens(ctx context.Context, xmlData string) error {
	dec := xml.NewDecoder(strings.NewReader(xmlData))
	dec.Strict = true
	for n := 0; ; n++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("XML token scan: %w", err)
		}
		if n > maxTokens {
			return fmt.Errorf("%w: XML token limit exceeded (max %d)", errForbiddenXML, maxTokens)
		}
		_, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("XML scan: %w", err)
		}
	}
}

func unmarshal(xmlData string) (*bpmncore.BPMNDefinitions, error) {
	dec := xml.NewDecoder(strings.NewReader(xmlData))
	dec.Strict = true
	var defs bpmncore.BPMNDefinitions
	if err := dec.Decode(&defs); err != nil {
		return nil, fmt.Errorf("XML decode: %w", err)
	}
	return &defs, nil
}
