package bpmn_compiler_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

const (
	w12aTokenLimit = 1_000_000
	w12aCalledLib  = "Process_Lib"
)

func w12aTokenDocument(tokens int) string {
	if tokens%2 == 0 {
		return "<r>" + strings.Repeat("<a/>", (tokens-2)/2) + "</r>"
	}
	return "<r>x" + strings.Repeat("<a/>", (tokens-3)/2) + "</r>"
}

func TestW12aCheckWellFormed_TextThatIsNotOneWellFormedDocument_WrapsMalformedBPMN(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		xml       string
		wantText  string
		forbidden bool
	}{
		{name: "syntax error: mismatched end tag", xml: "<a><b></a></b>", wantText: "XML scan"},
		{name: "syntax error: unquoted attribute", xml: "<a id=1/>", wantText: "XML scan"},
		{name: "syntax error: element never closed", xml: "<definitions><process>", wantText: "XML scan"},
		{name: "no root element: plain text", xml: "this is not xml", wantText: "found 0"},
		{name: "no root element: empty text", xml: "", wantText: "found 0"},
		{name: "no root element: only a comment", xml: "<!-- zzq nothing else -->", wantText: "found 0"},
		{name: "two root elements", xml: "<a/><b/>", wantText: "found 2"},
		{name: "document type declaration", xml: `<!DOCTYPE zzq_root [<!ENTITY zzq_entity "zzq_value">]><zzq_root/>`, forbidden: true, wantText: "DOCTYPE or entity"},
		{name: "document type declaration in lower case", xml: "<!doctype zzq_root><zzq_root/>", forbidden: true, wantText: "DOCTYPE or entity"},
		{name: "entity declaration inside an element", xml: `<zzq_root><!ENTITY zzq_entity "zzq_value"></zzq_root>`, forbidden: true, wantText: "DOCTYPE or entity"},
		{name: "one token past the limit", xml: w12aTokenDocument(w12aTokenLimit + 1), forbidden: true, wantText: "token limit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := bpmn_compiler.New().CheckWellFormed(context.Background(), tc.xml)

			require.Error(t, err)
			assert.ErrorIs(t, err, domain.ErrMalformedBPMN)
			assert.Equal(t, tc.forbidden, errors.Is(err, domain.ErrForbiddenXML), err.Error())
			assert.NotErrorIs(t, err, context.Canceled)
			assert.NotErrorIs(t, err, context.DeadlineExceeded)
			assert.Contains(t, err.Error(), tc.wantText)
		})
	}
}

func TestW12aCheckWellFormed_OneRootElementOfAnyName_ReturnsNil(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		xml  string
	}{
		{name: "an unrelated name", xml: "<zzq_anything/>"},
		{name: "a BPMN name with no namespaces", xml: "<definitions/>"},
		{name: "a prefixed name for an undeclared-looking namespace", xml: `<q:zzq_root xmlns:q="urn:zzq"/>`},
		{name: "text inside the root", xml: "<a>zzq text</a>"},
		{name: "declaration and comments around the root", xml: "<?xml version=\"1.0\"?><!-- c --><a x=\"1\"><b>text</b></a><!-- d -->"},
		{name: "whitespace around the root", xml: "\n  <a/>\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.NoError(t, bpmn_compiler.New().CheckWellFormed(context.Background(), tc.xml))
		})
	}
}

func TestW12aCheckWellFormed_ContextEndedBeforeTheScan_ReturnsTheContextErrorNotAMalformedDocument(t *testing.T) {
	t.Parallel()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, expire := context.WithDeadline(context.Background(), time.Unix(1, 0))
	t.Cleanup(expire)
	cases := []struct {
		name string
		ctx  context.Context
		want error
	}{
		{name: "cancelled", ctx: cancelled, want: context.Canceled},
		{name: "deadline already passed", ctx: expired, want: context.DeadlineExceeded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := bpmn_compiler.New().CheckWellFormed(tc.ctx, "<a/>")

			require.Error(t, err)
			assert.ErrorIs(t, err, tc.want)
			assert.NotErrorIs(t, err, domain.ErrMalformedBPMN)
			assert.NotErrorIs(t, err, domain.ErrForbiddenXML)
		})
	}
}

func TestW12aTokenLimit_ExactBoundary_OneMillionAcceptedOneMillionAndOneRefused(t *testing.T) {
	t.Parallel()
	atLimit := w12aTokenDocument(w12aTokenLimit)
	overLimit := w12aTokenDocument(w12aTokenLimit + 1)
	require.Greater(t, len(overLimit), len(atLimit))
	require.Less(t, len(overLimit), 10<<20, "the document must fit the request size limit")

	t.Run("well-formedness check accepts a million tokens", func(t *testing.T) {
		t.Parallel()
		assert.NoError(t, bpmn_compiler.New().CheckWellFormed(context.Background(), atLimit))
	})
	t.Run("well-formedness check refuses a million and one tokens as forbidden XML", func(t *testing.T) {
		t.Parallel()
		err := bpmn_compiler.New().CheckWellFormed(context.Background(), overLimit)
		require.Error(t, err)
		assert.ErrorIs(t, err, domain.ErrMalformedBPMN)
		assert.ErrorIs(t, err, domain.ErrForbiddenXML)
	})
	t.Run("validate gets past the token scan with a million tokens", func(t *testing.T) {
		t.Parallel()
		issues, err := bpmn_compiler.New().Validate(context.Background(), atLimit)
		require.NoError(t, err)
		require.Len(t, issues, 1, "the scan accepted the document, so only the missing namespace is reported")
		assert.Equal(t, domain.BPMNErrMissingNamespace, issues[0].Code)
	})
	t.Run("validate refuses a million and one tokens as forbidden XML", func(t *testing.T) {
		t.Parallel()
		issues, err := bpmn_compiler.New().Validate(context.Background(), overLimit)
		require.Error(t, err)
		assert.Empty(t, issues)
		assert.ErrorIs(t, err, domain.ErrMalformedBPMN)
		assert.ErrorIs(t, err, domain.ErrForbiddenXML)
		assert.Contains(t, err.Error(), "token limit")
	})
}

func TestW12aValidate_NamespaceNotDeclared_NamesTheMissingNamespace(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		xml        string
		wantNamed  string
		wantAbsent string
	}{
		{
			name:       "BPMN namespace not declared, Zeebe declared",
			xml:        `<definitions xmlns:zeebe="` + zeebeNS + `" id="D1"><process id="P1"/></definitions>`,
			wantNamed:  bpmnModelNS,
			wantAbsent: zeebeNS,
		},
		{
			name:       "Zeebe namespace not declared, BPMN declared",
			xml:        `<bpmn:definitions xmlns:bpmn="` + bpmnModelNS + `" id="D1"><bpmn:process id="P1"/></bpmn:definitions>`,
			wantNamed:  zeebeNS,
			wantAbsent: bpmnModelNS,
		},
		{
			name:       "BPMN namespace only in a comment and an attribute value",
			xml:        `<!-- ` + bpmnModelNS + ` --><definitions xmlns:zeebe="` + zeebeNS + `" targetNamespace="` + bpmnModelNS + `" id="D1"/>`,
			wantNamed:  bpmnModelNS,
			wantAbsent: zeebeNS,
		},
		{
			name:       "Zeebe namespace only in a comment and an attribute value",
			xml:        `<bpmn:definitions xmlns:bpmn="` + bpmnModelNS + `" documentation="` + zeebeNS + `" id="D1"><!-- ` + zeebeNS + ` --></bpmn:definitions>`,
			wantNamed:  zeebeNS,
			wantAbsent: bpmnModelNS,
		},
		{
			name:       "root element in another namespace that also declares both",
			xml:        `<x:definitions xmlns:x="urn:zzq" xmlns:bpmn="` + bpmnModelNS + `" xmlns:zeebe="` + zeebeNS + `" id="D1"/>`,
			wantNamed:  bpmnModelNS,
			wantAbsent: zeebeNS,
		},
		{
			name:       "plain text",
			xml:        "this is plain text",
			wantNamed:  bpmnModelNS,
			wantAbsent: zeebeNS,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			issues, err := bpmn_compiler.New().Validate(context.Background(), tc.xml)

			require.NoError(t, err, "a missing namespace is an issue, not a failed call")
			require.Len(t, issues, 1)
			assert.Equal(t, domain.BPMNErrMissingNamespace, issues[0].Code)
			assert.Equal(t, domain.SeverityError, issues[0].Severity)
			assert.Empty(t, issues[0].NodeID)
			assert.Contains(t, issues[0].Message, tc.wantNamed)
			assert.NotContains(t, issues[0].Message, tc.wantAbsent)
		})
	}
}

func w12aCallerDocument(calledElementAttrs string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="` + bpmnModelNS + `" xmlns:zeebe="` + zeebeNS + `" id="Def_Caller" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="Process_Main" isExecutable="true">
    <bpmn:startEvent id="Start_1"/>
    <bpmn:callActivity id="CA_1" name="Call lib"><bpmn:extensionElements><zeebe:calledElement ` + calledElementAttrs + `/></bpmn:extensionElements></bpmn:callActivity>
    <bpmn:endEvent id="End_1"/>
    <bpmn:sequenceFlow id="F1" sourceRef="Start_1" targetRef="CA_1"/>
    <bpmn:sequenceFlow id="F2" sourceRef="CA_1" targetRef="End_1"/>
  </bpmn:process>
</bpmn:definitions>`
}

func TestW12aModuleCalls_BindingThatCannotNameAModule_IsFlaggedOnTheCallWithItsReason(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		attrs      string
		wantReason string
	}{
		{
			name:       "expression process id",
			attrs:      `processId="= &quot;Process_&quot; + kind"`,
			wantReason: "names its module with an expression",
		},
		{
			name:       "version tag that is not a number",
			attrs:      `processId="` + w12aCalledLib + `" bindingType="versionTag" versionTag="v3"`,
			wantReason: `pins versionTag "v3"`,
		},
		{
			name:       "version tag missing",
			attrs:      `processId="` + w12aCalledLib + `" bindingType="versionTag"`,
			wantReason: `pins versionTag ""`,
		},
		{
			name:       "version tag zero",
			attrs:      `processId="` + w12aCalledLib + `" bindingType="versionTag" versionTag="0"`,
			wantReason: `pins versionTag "0"`,
		},
		{
			name:       "version tag negative",
			attrs:      `processId="` + w12aCalledLib + `" bindingType="versionTag" versionTag="-2"`,
			wantReason: `pins versionTag "-2"`,
		},
		{
			name:       "deployment binding for a process not in the file",
			attrs:      `processId="` + w12aCalledLib + `" bindingType="deployment"`,
			wantReason: "uses bindingType deployment",
		},
		{
			name:       "unknown binding type",
			attrs:      `processId="` + w12aCalledLib + `" bindingType="zzq_unknown"`,
			wantReason: `uses bindingType "zzq_unknown"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			calls, issues, err := bpmn_compiler.New().ModuleCalls(context.Background(), w12aCallerDocument(tc.attrs))

			require.NoError(t, err)
			assert.Empty(t, calls, "a call that cannot name a module is not looked up")
			require.Len(t, issues, 1)
			assert.Equal(t, domain.BPMNErrUnresolvedCalledElement, issues[0].Code)
			assert.Equal(t, "CA_1", issues[0].NodeID)
			assert.Equal(t, domain.SeverityError, issues[0].Severity)
			assert.Contains(t, issues[0].Message, tc.wantReason)
		})
	}
}

func TestW12aModuleCalls_BindingsThatNameAModule_AreListedWithoutAnIssue(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		attrs string
		want  domain.ModuleCall
	}{
		{
			name:  "no binding type means latest",
			attrs: `processId="` + w12aCalledLib + `"`,
			want:  domain.ModuleCall{CallID: "CA_1", ProcessID: w12aCalledLib},
		},
		{
			name:  "latest binding",
			attrs: `processId="` + w12aCalledLib + `" bindingType="latest"`,
			want:  domain.ModuleCall{CallID: "CA_1", ProcessID: w12aCalledLib},
		},
		{
			name:  "version tag of one",
			attrs: `processId="` + w12aCalledLib + `" bindingType="versionTag" versionTag="1"`,
			want:  domain.ModuleCall{CallID: "CA_1", ProcessID: w12aCalledLib, VersionNumber: 1},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			calls, issues, err := bpmn_compiler.New().ModuleCalls(context.Background(), w12aCallerDocument(tc.attrs))

			require.NoError(t, err)
			assert.Empty(t, issues)
			assert.Equal(t, []domain.ModuleCall{tc.want}, calls)
		})
	}
}
