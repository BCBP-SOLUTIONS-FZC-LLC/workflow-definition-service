package bpmn_compiler

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

const maxTokens = 50_000

func parse(ctx context.Context, xmlData string) (*bpmnDefinitions, error) {
	if err := securityScan(xmlData); err != nil {
		return nil, err
	}
	if err := countTokens(ctx, xmlData); err != nil {
		return nil, err
	}
	defs, err := unmarshal(xmlData)
	if err != nil {
		return nil, err
	}
	if defs.XMLName.Space != nsBPMN {
		return nil, fmt.Errorf(
			"missing required BPMN namespace (got %q, want %q)",
			defs.XMLName.Space, nsBPMN,
		)
	}
	return defs, nil
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
		return fmt.Errorf("forbidden XML construct: DOCTYPE or entity declaration detected")
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
			return fmt.Errorf("XML token limit exceeded (max %d)", maxTokens)
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

func unmarshal(xmlData string) (*bpmnDefinitions, error) {
	dec := xml.NewDecoder(strings.NewReader(xmlData))
	dec.Strict = true
	var defs bpmnDefinitions
	if err := dec.Decode(&defs); err != nil {
		return nil, fmt.Errorf("XML decode: %w", err)
	}
	return &defs, nil
}
