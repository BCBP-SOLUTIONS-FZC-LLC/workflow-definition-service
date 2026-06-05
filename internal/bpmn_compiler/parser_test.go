package bpmn_compiler

import (
	"context"
	"errors"
	"strings"
	"testing"
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
	if err == nil {
		t.Error("parse() should return error when BPMN namespace is missing")
	}
}

func TestParse_MalformedXML(t *testing.T) {
	xmlData := `<?xml version="1.0"?><bpmn:definitions xmlns:bpmn="` + nsBPMN + `"><unclosed>`
	_, err := parse(context.Background(), xmlData)
	if err == nil {
		t.Error("parse() should return error for malformed XML")
	}
}

func TestParse_ValidMinimal(t *testing.T) {
	xmlData := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<bpmn:definitions xmlns:bpmn="` + nsBPMN + `" id="D1"/>`
	defs, err := parse(context.Background(), xmlData)
	if err != nil {
		t.Fatalf("parse() unexpected error: %v", err)
	}
	if defs == nil {
		t.Error("parse() returned nil definitions for valid XML")
	}
}
