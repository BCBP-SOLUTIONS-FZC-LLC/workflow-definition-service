package bpmn_compiler_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func sgALibraryCall(attrs string) string {
	return pinned(mainWithCalls([]callSite{{ID: "CA_1", Lane: "Engineering", Calls: "Process_Lib"}}), "Process_Lib", "attrs:"+attrs)
}

func TestModuleCalls_VersionTagForms_AreAcceptedOrRefusedNamingTheTag(t *testing.T) {
	t.Parallel()
	cases := []struct {
		tag  string
		want int32
	}{
		{" 3 ", 3}, {"+3", 3}, {"03", 3}, {"2147483647", 2147483647},
		{"", 0}, {"0", 0}, {"-1", 0}, {"1.5", 0}, {"abc", 0}, {"2147483648", 0},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("tag %q", tc.tag), func(t *testing.T) {
			t.Parallel()
			doc := sgALibraryCall(fmt.Sprintf(`processId="Process_Lib" bindingType="versionTag" versionTag=%q`, tc.tag))

			calls, issues, err := bpmn_compiler.New().ModuleCalls(context.Background(), doc)

			require.NoError(t, err)
			if tc.want > 0 {
				assert.Equal(t, []domain.ModuleCall{{CallID: "CA_1", ProcessID: "Process_Lib", VersionNumber: tc.want}}, calls)
				assert.Empty(t, issues)
				return
			}
			assert.Empty(t, calls)
			require.Len(t, issues, 1, "issues = %+v", issues)
			assert.Equal(t, domain.BPMNErrUnresolvedCalledElement, issues[0].Code)
			assert.Equal(t, "CA_1", issues[0].NodeID)
			assert.Equal(t, domain.SeverityError, issues[0].Severity)
			assert.Contains(t, issues[0].Message, fmt.Sprintf("versionTag %q", tc.tag))
		})
	}
}

func TestModuleCalls_BindingTypes_AreMatchedExactly(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		attrs     string
		wantCall  bool
		wantIssue string
	}{
		{"no bindingType is the latest version", `processId="Process_Lib"`, true, ""},
		{"latest is the latest version", `processId="Process_Lib" bindingType="latest"`, true, ""},
		{"a capitalised Latest is unknown", `processId="Process_Lib" bindingType="Latest"`, false, `bindingType "Latest"`},
		{"a padded latest is unknown", `processId="Process_Lib" bindingType=" latest"`, false, `bindingType " latest"`},
		{"an unknown binding is refused", `processId="Process_Lib" bindingType="bogus"`, false, `bindingType "bogus"`},
		{"deployment of a process outside the file is refused", `processId="Process_Lib" bindingType="deployment"`, false, "bindingType deployment"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			calls, issues, err := bpmn_compiler.New().ModuleCalls(context.Background(), sgALibraryCall(tc.attrs))

			require.NoError(t, err)
			if tc.wantCall {
				assert.Equal(t, []domain.ModuleCall{{CallID: "CA_1", ProcessID: "Process_Lib"}}, calls)
				assert.Empty(t, issues)
				return
			}
			assert.Empty(t, calls)
			require.Len(t, issues, 1, "issues = %+v", issues)
			assert.Equal(t, domain.BPMNErrUnresolvedCalledElement, issues[0].Code)
			assert.Equal(t, "CA_1", issues[0].NodeID)
			assert.Contains(t, issues[0].Message, tc.wantIssue)
		})
	}
}

func TestModuleCalls_CallWithoutAProcessIDNamesNoModule(t *testing.T) {
	t.Parallel()
	doc := mainWithCalls([]callSite{{ID: "CA_1", Lane: "Engineering", Calls: ""}})

	calls, issues, err := bpmn_compiler.New().ModuleCalls(context.Background(), doc)

	require.NoError(t, err)
	assert.Empty(t, calls)
	assert.Empty(t, issues)
}

func TestCompileModule_Declarations_AreSortedUniqueAndSkipEmptyNames(t *testing.T) {
	t.Parallel()
	declared := `
  <bpmn:message id="M1" name="zeta"/>
  <bpmn:message id="M2" name="alpha"/>
  <bpmn:message id="M3" name="alpha"/>
  <bpmn:message id="M4" name=""/>
  <bpmn:error id="E1" errorCode="E_TWO"/>
  <bpmn:error id="E2" errorCode="E_ONE"/>
  <bpmn:error id="E3" errorCode="E_ONE"/>
  <bpmn:error id="E4" errorCode=""/>`
	doc := strings.Replace(moduleDocument(moduleWithTask("Process_Decl", validCandidateUser)),
		`targetNamespace="http://bpmn.io/schema/bpmn">`, `targetNamespace="http://bpmn.io/schema/bpmn">`+declared, 1)

	_, decl, err := bpmn_compiler.New().CompileModule(context.Background(), doc, "Process_Decl")

	require.NoError(t, err)
	assert.Equal(t, []string{"alpha", "zeta"}, decl.Messages)
	assert.Equal(t, []string{"E_ONE", "E_TWO"}, decl.ErrorCodes)
}

func TestValidate_ProcessWithoutAStartEvent_NeedsACollaborationToGetAnImplicitOne(t *testing.T) {
	t.Parallel()
	noStart := sgAMainWithTaskInLane(opsDeptID, userTaskXML("Task_A"))
	noStart = strings.Replace(noStart, "    <bpmn:startEvent id=\"Start_1\"/>\n", "", 1)
	noStart = strings.Replace(noStart, flowXML("F1", "Start_1", "Task_A"), "", 1)
	noStart = strings.Replace(noStart, "<bpmn:flowNodeRef>Start_1</bpmn:flowNodeRef>", "", 1)
	noStart = strings.Replace(noStart, shape("Start_1"), "", 1)
	noStart = strings.Replace(noStart, edge("F1"), "", 1)
	inCollaboration := strings.Replace(noStart, `  <bpmn:process id="Process_Main"`,
		`  <bpmn:collaboration id="Collab"><bpmn:participant id="P_Main" name="Main" processRef="Process_Main"/></bpmn:collaboration>
  <bpmn:process id="Process_Main"`, 1)
	c := bpmn_compiler.New()

	alone, err := c.Validate(context.Background(), noStart)
	require.NoError(t, err)
	assert.NotNil(t, findCode(blockingErrors(alone), domain.BPMNErrNoStartEvent), "errors = %+v", alone)

	pooled, err := c.Validate(context.Background(), inCollaboration)
	require.NoError(t, err)
	assert.Nil(t, findCode(pooled, domain.BPMNErrNoStartEvent), "errors = %+v", pooled)
	assert.Empty(t, blockingErrors(pooled), "errors = %+v", pooled)
}
