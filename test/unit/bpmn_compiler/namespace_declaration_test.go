package bpmn_compiler_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

const (
	bpmnModelNS = "http://www.omg.org/spec/BPMN/20100524/MODEL"
	zeebeNS     = "http://camunda.org/schema/zeebe/1.0"
)

func TestValidate_NamespaceMustBeDeclaredNotJustNamed(t *testing.T) {
	cases := []struct {
		name        string
		xml         string
		wantMissing bool
	}{
		{
			name: "both namespaces named only in a comment",
			xml: `<!-- ` + bpmnModelNS + ` ` + zeebeNS + ` -->
<definitions xmlns="http://wrong.example/ns" id="D1"><process id="P1"/></definitions>`,
			wantMissing: true,
		},
		{
			name: "bpmn namespace named only in a comment",
			xml: `<?xml version="1.0"?>
<!-- ` + bpmnModelNS + ` -->
<d:definitions xmlns:d="http://wrong.example/ns" xmlns:zeebe="` + zeebeNS + `" id="D1"/>`,
			wantMissing: true,
		},
		{
			name: "zeebe namespace named only in a comment",
			xml: `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="` + bpmnModelNS + `" id="D1">
  <!-- ` + zeebeNS + ` -->
  <bpmn:process id="P1" isExecutable="true"/>
</bpmn:definitions>`,
			wantMissing: true,
		},
		{
			name: "namespaces named in an attribute value and text, not declared",
			xml: `<?xml version="1.0"?>
<definitions id="D1" targetNamespace="` + bpmnModelNS + `" documentation="` + zeebeNS + `"/>`,
			wantMissing: true,
		},
		{
			name:        "namespace declared on the root with a prefix",
			xml:         minimalBPMN(`<bpmn:startEvent id="StartEvent_1"/>`),
			wantMissing: false,
		},
		{
			name: "bpmn namespace declared as the default namespace of the root",
			xml: `<?xml version="1.0"?>
<!-- a leading comment -->
<definitions xmlns="` + bpmnModelNS + `" xmlns:zeebe="` + zeebeNS + `" id="D1">
  <process id="P1" isExecutable="true"/>
</definitions>`,
			wantMissing: false,
		},
		{
			name: "zeebe namespace declared on a nested element, as a bundled document writes it",
			xml: `<?xml version="1.0"?>
<definitions xmlns="` + bpmnModelNS + `" id="D1">
  <process id="P1" isExecutable="true">
    <extensionElements><properties xmlns="` + zeebeNS + `"/></extensionElements>
  </process>
</definitions>`,
			wantMissing: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			issues, err := bpmn_compiler.New().Validate(context.Background(), tc.xml)

			require.NoError(t, err, "a document that parses is reported as an issue, never as a 400")
			var missing []domain.BPMNValidationError
			for _, issue := range issues {
				if issue.Code == domain.BPMNErrMissingNamespace {
					missing = append(missing, issue)
				}
			}
			if !tc.wantMissing {
				assert.Empty(t, missing)
				return
			}
			require.Len(t, missing, 1)
			assert.Equal(t, domain.SeverityError, missing[0].Severity)
			assert.Empty(t, missing[0].NodeID)
			assert.True(t, strings.Contains(missing[0].Message, "namespace"), missing[0].Message)
		})
	}
}
