package bpmn_compiler

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/pkg/enums"
)

const (
	nsBPMN   = "http://www.omg.org/spec/BPMN/20100524/MODEL"
	nsZeebe  = "http://camunda.org/schema/zeebe/1.0"
	nsBPMNDI = "http://www.omg.org/spec/BPMN/20100524/DI"
)

// 1M tokens comfortably covers the largest BPMN diagrams this service accepts
// (the 10 MB request-body cap enforced upstream), while still bounding a
// pathological document that omits an explicit DOCTYPE/ENTITY declaration.
const maxTokens = 1_000_000

var errMissingNamespace = errors.New("missing required BPMN or Zeebe namespace")
var errForbiddenXML = errors.New("forbidden XML construct")

// Every BPMN element this compiler can see falls into exactly one tier, each
// producing a different outcome for the caller:
//   - tier1Elems: recognized BPMN vocabulary (workflow-models' AllowedBPMNElements).
//   - tier2Elems: tier1, but with no compile handler yet — UNSUPPORTED_ELEMENT,
//     i.e. "on the roadmap."
//   - tier3Elems: will never be supported — REJECTED_ELEMENT.
//   - anything in none of the three: not recognized at all — also REJECTED_ELEMENT,
//     the backstop for a typo or genuinely unknown tag.
var tier3Elems map[string]struct{}
var tier2Elems map[string]struct{}
var tier1Elems map[string]struct{}

func init() {
	tier3Elems = map[string]struct{}{
		"serviceTask": {}, "scriptTask": {}, "businessRuleTask": {},
		"transaction": {}, "adHocSubProcess": {},

		"complexGateway": {},

		"terminateEventDefinition": {}, "compensateEventDefinition": {},
		"cancelEventDefinition": {}, "escalationEventDefinition": {},
		"conditionalEventDefinition": {}, "linkEventDefinition": {},
		"signalEventDefinition": {},

		"standardLoopCharacteristics": {},

		"conversation": {}, "choreography": {},

		"dataObject": {}, "dataObjectReference": {},
	}
	tier2Elems = map[string]struct{}{
		"inclusiveGateway":       {},
		"eventBasedGateway":      {},
		"intermediateCatchEvent": {},
	}
	tier1Elems = make(map[string]struct{}, len(enums.AllowedBPMNElements))
	for _, elem := range enums.AllowedBPMNElements {
		tier1Elems[elem] = struct{}{}
	}
}

func parse(ctx context.Context, xmlData string) (*bpmncore.BPMNDefinitions, error) {
	if err := securityScan(xmlData); err != nil {
		return nil, err
	}
	if err := countTokens(ctx, xmlData); err != nil {
		return nil, err
	}
	if err := requireNamespaces(xmlData); err != nil {
		return nil, err
	}
	return unmarshal(xmlData)
}

func requireNamespaces(xmlData string) error {
	if !strings.Contains(xmlData, nsBPMN) {
		return fmt.Errorf("%w: the BPMN namespace (%s) is not declared", errMissingNamespace, nsBPMN)
	}
	if !strings.Contains(xmlData, nsZeebe) {
		return fmt.Errorf("%w: the Zeebe namespace (%s) is not declared", errMissingNamespace, nsZeebe)
	}
	return nil
}

func scanRejected(xmlData string) []domain.BPMNValidationError {
	dec := xml.NewDecoder(strings.NewReader(xmlData))
	dec.Strict = true
	var errs []domain.BPMNValidationError
	for {
		tok, tokenErr := dec.Token()
		if tokenErr != nil {
			return errs
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Space != nsBPMN {
			continue
		}
		elemErr, abort := classifyElement(dec, se)
		if abort {
			return errs
		}
		if elemErr != nil {
			errs = append(errs, *elemErr)
		}
	}
}

func classifyElement(dec *xml.Decoder, se xml.StartElement) (elemErr *domain.BPMNValidationError, abort bool) {
	if se.Name.Local == "serviceTask" {
		return classifyServiceTask(dec, se)
	}
	if _, rejected := tier3Elems[se.Name.Local]; rejected {
		return rejectedElementErr(se, domain.BPMNErrRejectedElement, "not supported (§4.1.2 Tier 3)"), false
	}
	if _, unsupported := tier2Elems[se.Name.Local]; unsupported {
		return rejectedElementErr(se, domain.BPMNErrUnsupportedElement, "planned but not yet implemented (§4.1.2 Tier 2)"), false
	}
	if _, allowed := tier1Elems[se.Name.Local]; !allowed {
		return rejectedElementErr(se, domain.BPMNErrRejectedElement, "not part of the recognized element vocabulary (§4.1.2/§10.17)"), false
	}
	return nil, false
}

func classifyServiceTask(dec *xml.Decoder, se xml.StartElement) (elemErr *domain.BPMNValidationError, abort bool) {
	_, isConnector, scanErr := scanServiceTaskConnectorType(dec)
	if scanErr != nil {
		return nil, true
	}
	if isConnector {
		return nil, false
	}
	return rejectedElementErr(se, domain.BPMNErrRejectedElement, "not supported (§4.1.2 Tier 3)"), false
}

func rejectedElementErr(se xml.StartElement, code domain.BPMNErrorCode, reason string) *domain.BPMNValidationError {
	return &domain.BPMNValidationError{
		Code:     code,
		NodeID:   elementID(se),
		Message:  fmt.Sprintf("BPMN element <%s> is %s", se.Name.Local, reason),
		Severity: domain.SeverityError,
	}
}

func scanServiceTaskConnectorType(dec *xml.Decoder) (connectorType string, isConnector bool, err error) {
	depth := 1
	for depth > 0 {
		tok, tokenErr := dec.Token()
		if tokenErr != nil {
			return "", false, fmt.Errorf("scan service task subtree: %w", tokenErr)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Space == nsBPMN && t.Name.Local == "serviceTask" {
				depth++
			}
			isDirectChildOfOuterTask := depth == 1
			isTaskDefinition := t.Name.Space == nsZeebe && t.Name.Local == "taskDefinition"
			if !isConnector && isDirectChildOfOuterTask && isTaskDefinition {
				for _, a := range t.Attr {
					if a.Name.Local != "type" {
						continue
					}
					name, hasConnectorPrefix := strings.CutPrefix(a.Value, bpmncore.ConnectorTaskDefPrefix)
					if hasConnectorPrefix && name != "" {
						connectorType, isConnector = name, true
					}
				}
			}
		case xml.EndElement:
			if t.Name.Space == nsBPMN && t.Name.Local == "serviceTask" {
				depth--
			}
		}
	}
	return connectorType, isConnector, nil
}

func elementID(se xml.StartElement) string {
	for _, a := range se.Attr {
		if a.Name.Local == "id" {
			return a.Value
		}
	}
	return se.Name.Local
}

const docTypeScanWindowBytes = 4096

func securityScan(xmlData string) error {
	head := xmlData
	if len(head) > docTypeScanWindowBytes {
		head = head[:docTypeScanWindowBytes]
	}
	upper := strings.ToUpper(head)
	if strings.Contains(upper, "<!DOCTYPE") || strings.Contains(upper, "<!ENTITY") {
		return fmt.Errorf("%w: DOCTYPE or entity declaration detected", errForbiddenXML)
	}
	return nil
}

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
	for i := range defs.Processes {
		p := &defs.Processes[i]
		promoteTasks(&p.UserTasks, &p.GenericTasks, &p.ServiceTasks)
		p.SequenceFlows = stripEmptyRefFlows(p.SequenceFlows)
		for j := range p.SubProcesses {
			sp := &p.SubProcesses[j]
			promoteTasks(&sp.UserTasks, &sp.GenericTasks, &sp.ServiceTasks)
			sp.SequenceFlows = stripEmptyRefFlows(sp.SequenceFlows)
		}
	}
	return &defs, nil
}

func promoteTasks(userTasks, genericTasks, serviceTasks *[]bpmncore.BPMNUserTask) {
	*userTasks = append(*userTasks, *genericTasks...)
	*genericTasks = nil
	*userTasks = append(*userTasks, connectorServiceTasks(*serviceTasks)...)
	*serviceTasks = nil
}

// connectorServiceTasks is a defensive filter: only serviceTasks already
// classified as connectors by scanRejected may reach the compiled pipeline.
func connectorServiceTasks(tasks []bpmncore.BPMNUserTask) []bpmncore.BPMNUserTask {
	var out []bpmncore.BPMNUserTask
	for _, t := range tasks {
		if _, ok := bpmncore.ConnectorType(t.ExtensionElements); ok {
			out = append(out, t)
		}
	}
	return out
}

func stripEmptyRefFlows(flows []bpmncore.BPMNSequenceFlow) []bpmncore.BPMNSequenceFlow {
	out := flows[:0:len(flows)]
	for _, f := range flows {
		if f.SourceRef != "" && f.TargetRef != "" {
			out = append(out, f)
		}
	}
	return out
}
