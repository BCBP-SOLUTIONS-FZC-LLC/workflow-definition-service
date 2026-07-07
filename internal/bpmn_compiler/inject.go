package bpmn_compiler

import (
	"context"
	"encoding/xml"
	"fmt"
)

// InjectTemplates merges module BPMN processes into the main BPMN definitions.
// Only BPMNProcess elements are merged — Diagrams and Collaboration from modules
// are intentionally dropped so module diagram pools don't clutter the main diagram.
//
// Each module XML is run through the same security checks as the main BPMN
// (DOCTYPE/entity guard + XML-bomb token cap) before its processes are merged.
func InjectTemplates(mainXML string, moduleXMLs []string) (string, error) {
	if len(moduleXMLs) == 0 {
		return mainXML, nil
	}
	defs, err := unmarshal(mainXML)
	if err != nil {
		return "", fmt.Errorf("inject: parse main BPMN: %w", err)
	}
	for i, modXML := range moduleXMLs {
		// parse runs securityScan + countTokens + namespace validation before unmarshal.
		modDefs, err := parse(context.Background(), modXML)
		if err != nil {
			return "", fmt.Errorf("inject: parse module[%d]: %w", i, err)
		}
		defs.Processes = append(defs.Processes, modDefs.Processes...)
		// Diagrams intentionally NOT merged.
	}
	out, err := xml.Marshal(defs)
	if err != nil {
		return "", fmt.Errorf("inject: marshal bundled BPMN: %w", err)
	}
	return xml.Header + string(out), nil
}
