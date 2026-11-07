package bpmn_compiler

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

// compOneDoc wraps body in a definitions element that declares the BPMN and Zeebe namespaces.
func compOneDoc(body string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="` + nsBPMN + `" xmlns:zeebe="` + nsZeebe + `" id="D1" targetNamespace="http://bpmn.io/schema/bpmn">
` + body + `
</bpmn:definitions>`
}

// compOneModuleProcess is a laneless process holding one user task; the task names a default user.
func compOneModuleProcess(id, nameAttr string) string {
	return `<bpmn:process id="` + id + `"` + nameAttr + ` isExecutable="true">
  <bpmn:startEvent id="` + id + `_Start"/>
  <bpmn:userTask id="` + id + `_Task">
    <bpmn:extensionElements>
      <zeebe:taskDefinition type="prep"/>
      <zeebe:assignmentDefinition candidateGroups="preparator" candidateUsers="` + aliceUUID + `"/>
    </bpmn:extensionElements>
  </bpmn:userTask>
  <bpmn:endEvent id="` + id + `_End"/>
  <bpmn:sequenceFlow id="` + id + `_F1" sourceRef="` + id + `_Start" targetRef="` + id + `_Task"/>
  <bpmn:sequenceFlow id="` + id + `_F2" sourceRef="` + id + `_Task" targetRef="` + id + `_End"/>
</bpmn:process>`
}

// compOneValidWorkflow is a laned single-process workflow that validates and compiles without issues.
const compOneValidWorkflow = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="` + nsBPMN + `"
  xmlns:bpmndi="` + nsBPMNDI + `"
  xmlns:zeebe="` + nsZeebe + `"
  id="D1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="P1" name="Test" isExecutable="true">
    <bpmn:laneSet id="LS"><bpmn:lane id="L1" name="ops"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="bd056344-b10d-5b31-8903-9f98dea26c6b"/></zeebe:properties></bpmn:extensionElements>
      <bpmn:flowNodeRef>S1</bpmn:flowNodeRef>
      <bpmn:flowNodeRef>T1</bpmn:flowNodeRef>
      <bpmn:flowNodeRef>E1</bpmn:flowNodeRef>
    </bpmn:lane></bpmn:laneSet>
    <bpmn:startEvent id="S1"><bpmn:outgoing>F1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:userTask id="T1" name="Task">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="reviewer" candidateUsers="` + aliceUUID + `"/>
      </bpmn:extensionElements>
      <bpmn:incoming>F1</bpmn:incoming><bpmn:outgoing>F2</bpmn:outgoing>
    </bpmn:userTask>
    <bpmn:endEvent id="E1"><bpmn:incoming>F2</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="F1" sourceRef="S1" targetRef="T1"/>
    <bpmn:sequenceFlow id="F2" sourceRef="T1" targetRef="E1"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BD">
    <bpmndi:BPMNPlane id="BP" bpmnElement="P1">
      <bpmndi:BPMNShape id="Sh_S1" bpmnElement="S1"/>
      <bpmndi:BPMNShape id="Sh_T1" bpmnElement="T1"/>
      <bpmndi:BPMNShape id="Sh_E1" bpmnElement="E1"/>
      <bpmndi:BPMNEdge id="Ed_F1" bpmnElement="F1"/>
      <bpmndi:BPMNEdge id="Ed_F2" bpmnElement="F2"/>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`

var errCompOneCompile = errors.New("compile refused by the test handler")

// compOneFailingTaskHandler accepts every user task in validation and fails when compiling one.
type compOneFailingTaskHandler struct{}

func (compOneFailingTaskHandler) NodeType() bpmncore.FlowNodeType { return bpmncore.NodeTypeUserTask }
func (compOneFailingTaskHandler) Validate(string, *bpmncore.BPMNProcess, *bpmncore.Graph, *bpmncore.BPMNDefinitions, map[string]bpmncore.StageTypeHandler, map[bpmncore.FlowNodeType]bpmncore.ElementHandler) []domain.BPMNValidationError {
	return nil
}
func (compOneFailingTaskHandler) Compile(string, *bpmncore.CompileState) error {
	return errCompOneCompile
}

func TestScanRejected_BreaksAfterRejectedElement_ReturnsIssuesFoundSoFar(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "mismatched end tag after two rejected elements",
			body: `<bpmn:process id="P"><bpmn:scriptTask id="Script1"/><bpmn:businessRuleTask id="Rule1"></bpmn:oops>`,
			want: []string{"Script1", "Rule1"},
		},
		{
			name: "document ends inside a service task",
			body: `<bpmn:process id="P"><bpmn:scriptTask id="Script1"/><bpmn:serviceTask id="Svc1"><bpmn:documentation>`,
			want: []string{"Script1"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := scanRejected(compOneDoc(tc.body))

			ids := make([]string, 0, len(got))
			for _, issue := range got {
				ids = append(ids, issue.NodeID)
				assert.Equal(t, domain.BPMNErrRejectedElement, issue.Code)
				assert.Equal(t, domain.SeverityError, issue.Severity)
			}
			assert.Equal(t, tc.want, ids)
		})
	}
}

func TestExecutableProcesses_CalledOnlyByIgnoredPool_IsStillARoot(t *testing.T) {
	t.Parallel()

	ignored := `<bpmn:extensionElements><zeebe:properties><zeebe:property name="ignore" value="true"/></zeebe:properties></bpmn:extensionElements>`
	body := `<bpmn:process id="Runs" isExecutable="true"><bpmn:startEvent id="RunsStart"/></bpmn:process>
<bpmn:process id="External" isExecutable="true">` + ignored + `
  <bpmn:callActivity id="CallC"><bpmn:extensionElements><zeebe:calledElement processId="Orphan"/></bpmn:extensionElements></bpmn:callActivity>
</bpmn:process>
<bpmn:process id="Orphan" isExecutable="true"><bpmn:startEvent id="OrphanStart"/></bpmn:process>`

	defs, err := parse(context.Background(), compOneDoc(body))
	require.NoError(t, err)

	roots := executableProcesses(defs)
	ids := make([]string, 0, len(roots))
	for _, p := range roots {
		ids = append(ids, p.ID)
	}
	assert.Equal(t, []string{"Runs", "Orphan"}, ids)

	root, rootErr := runnableRoot(defs)
	assert.Nil(t, root)
	require.NotNil(t, rootErr)
	assert.Equal(t, domain.BPMNErrMultipleProcesses, rootErr.Code)
	assert.Equal(t, domain.SeverityError, rootErr.Severity)
	assert.Contains(t, rootErr.Message, "2 pools would run")
}

func TestValidateForPublish_CompileFailsWithoutValidationError_ReturnsThatErrorAndNoIssues(t *testing.T) {
	t.Parallel()

	c := NewCompiler(WithElementHandler(compOneFailingTaskHandler{}))

	issues, err := c.Validate(context.Background(), compOneValidWorkflow)
	require.NoError(t, err)
	require.Empty(t, issues, "the document must pass validation so the failure comes from compiling")

	issues, err = c.ValidateForPublish(context.Background(), compOneValidWorkflow)

	require.ErrorIs(t, err, errCompOneCompile)
	var failed *domain.ValidationFailedError
	assert.False(t, errors.As(err, &failed), "a compile failure is not a validation failure")
	assert.Nil(t, issues)
}

func TestValidateModule_ProcessIDAbsentFromFile_ReturnsPlainError(t *testing.T) {
	t.Parallel()

	c := New()
	xmlData := compOneDoc(compOneModuleProcess("Present", ""))
	wantMsg := `bpmn validate-module: process "Absent" not in the document`

	for _, tc := range []struct {
		name      string
		call      func() ([]domain.BPMNValidationError, error)
		hasIssues bool
	}{
		{"validateModule", func() ([]domain.BPMNValidationError, error) {
			return c.validateModule(context.Background(), xmlData, "Absent", false)
		}, true},
		{"ValidateModule", func() ([]domain.BPMNValidationError, error) {
			return c.ValidateModule(context.Background(), xmlData, "Absent", false)
		}, true},
		{"CompileModule", func() ([]domain.BPMNValidationError, error) {
			_, _, err := c.CompileModule(context.Background(), xmlData, "Absent")
			return nil, err
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			issues, err := tc.call()

			require.Error(t, err)
			assert.Equal(t, wantMsg, err.Error())
			if tc.hasIssues {
				assert.Nil(t, issues)
			}
			var failed *domain.ValidationFailedError
			assert.False(t, errors.As(err, &failed))
			assert.False(t, errors.Is(err, domain.ErrMalformedBPMN))
		})
	}
}

func TestValidateModule_MissingNamespace_IsReportedAsIssueNotError(t *testing.T) {
	t.Parallel()

	issues, err := New().validateModule(context.Background(), `<?xml version="1.0"?><foo/>`, "Absent", false)

	require.NoError(t, err)
	require.Len(t, issues, 1)
	assert.Equal(t, domain.BPMNErrMissingNamespace, issues[0].Code)
	assert.Equal(t, domain.SeverityError, issues[0].Severity)
}

func TestBundleModules_ModuleFileWithWrongProcessCount_ReturnsErrorNamingPlanAndCount(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		moduleXML string
		wantMsg   string
	}{
		{
			name:      "no process",
			moduleXML: compOneDoc(""),
			wantMsg:   "bundle: module ModuleProc@v2 holds 0 processes, want 1",
		},
		{
			name:      "two processes",
			moduleXML: compOneDoc(compOneModuleProcess("ModuleProc", "") + "\n" + compOneModuleProcess("Other", "")),
			wantMsg:   "bundle: module ModuleProc@v2 holds 2 processes, want 1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			module := domain.ResolvedModule{ProcessID: "ModuleProc", VersionNumber: 2, PlanName: "ModuleProc@v2", BPMNXML: tc.moduleXML}

			out, err := bundleModules(bundleMainXML, []domain.ResolvedModule{module})

			require.Error(t, err)
			assert.Equal(t, tc.wantMsg, err.Error())
			assert.Empty(t, out)
			var failed *domain.ValidationFailedError
			assert.False(t, errors.As(err, &failed))
		})
	}
}

func TestModuleEntryPoints_ForbiddenOrMalformedXML_ClassifiedWithStageName(t *testing.T) {
	t.Parallel()

	forbidden := `<?xml version="1.0"?><!DOCTYPE foo [<!ENTITY x "y">]><root/>`
	malformed := compOneDoc(`<bpmn:process id="P"><unclosed>`)
	c := New()

	cases := []struct {
		name          string
		xml           string
		call          func(xmlData string) error
		wantPrefix    string
		wantForbidden bool
	}{
		{
			name: "validateModule forbidden",
			xml:  forbidden,
			call: func(x string) error {
				_, err := c.validateModule(context.Background(), x, "P", false)
				return err
			},
			wantPrefix:    "bpmn validate-module: ",
			wantForbidden: true,
		},
		{
			name: "validateModule malformed",
			xml:  malformed,
			call: func(x string) error {
				_, err := c.validateModule(context.Background(), x, "P", false)
				return err
			},
			wantPrefix: "bpmn validate-module: ",
		},
		{
			name: "ValidateModule forbidden",
			xml:  forbidden,
			call: func(x string) error {
				_, err := c.ValidateModule(context.Background(), x, "P", false)
				return err
			},
			wantPrefix:    "bpmn validate-module: ",
			wantForbidden: true,
		},
		{
			name: "compileModule forbidden",
			xml:  forbidden,
			call: func(x string) error {
				_, _, err := c.compileModule(context.Background(), x, "P")
				return err
			},
			wantPrefix:    "bpmn compile-module: ",
			wantForbidden: true,
		},
		{
			name: "compileModule malformed",
			xml:  malformed,
			call: func(x string) error {
				_, _, err := c.compileModule(context.Background(), x, "P")
				return err
			},
			wantPrefix: "bpmn compile-module: ",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.call(tc.xml)

			require.Error(t, err)
			assert.True(t, strings.HasPrefix(err.Error(), tc.wantPrefix), "got %q", err.Error())
			assert.ErrorIs(t, err, domain.ErrMalformedBPMN)
			assert.Equal(t, tc.wantForbidden, errors.Is(err, domain.ErrForbiddenXML))
		})
	}
}

func TestCompileModule_PlanNameIsProcessNameElseProcessID(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		nameAttr string
		want     string
	}{
		{name: "unnamed process", nameAttr: "", want: "ModProc"},
		{name: "named process", nameAttr: ` name="Prep module"`, want: "Prep module"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			xmlData := compOneDoc(compOneModuleProcess("ModProc", tc.nameAttr))

			collab, _, err := New().compileModule(context.Background(), xmlData, "ModProc")

			require.NoError(t, err)
			require.NotNil(t, collab)
			assert.Equal(t, tc.want, collab.MainPlan)
			require.NotEmpty(t, collab.Plans)
			assert.Equal(t, tc.want, collab.Plans[0].Name)
		})
	}
}

func TestCompileModule_LanelessModule_PlanCarriesOpenDepartmentSlot(t *testing.T) {
	t.Parallel()

	xmlData := compOneDoc(compOneModuleProcess("ModProc", ""))

	collab, _, err := New().compileModule(context.Background(), xmlData, "ModProc")

	require.NoError(t, err)
	require.NotEmpty(t, collab.Plans)
	var slots []string
	for _, d := range collab.Plans[0].Departments {
		if d.ID == "ModProc" {
			slots = append(slots, d.Label)
		}
	}
	assert.Equal(t, []string{"ModProc"}, slots)
}

func TestCompileModule_CompileFailure_IsReturnedWrappedWithStageName(t *testing.T) {
	t.Parallel()

	c := NewCompiler(WithElementHandler(compOneFailingTaskHandler{}))
	xmlData := compOneDoc(compOneModuleProcess("ModProc", ""))

	collab, decl, err := c.compileModule(context.Background(), xmlData, "ModProc")

	require.ErrorIs(t, err, errCompOneCompile)
	assert.True(t, strings.HasPrefix(err.Error(), "bpmn compile-module: "), "got %q", err.Error())
	var failed *domain.ValidationFailedError
	assert.False(t, errors.As(err, &failed))
	assert.Nil(t, collab)
	assert.Equal(t, domain.ModuleDeclarations{}, decl)
}

func TestParse_CalledProcesses_AreMarkedAsModuleProcesses(t *testing.T) {
	t.Parallel()

	call := func(id, calledAttr string) string {
		return `<bpmn:callActivity id="` + id + `"><bpmn:extensionElements>` + calledAttr + `</bpmn:extensionElements></bpmn:callActivity>`
	}
	cases := []struct {
		name string
		body string
		want map[string]bool
	}{
		{
			name: "call to a process in the same file marks it",
			body: `<bpmn:process id="Main">` + call("CA1", `<zeebe:calledElement processId="Body"/>`) + `</bpmn:process>
<bpmn:process id="Body"/><bpmn:process id="Unrelated"/>`,
			want: map[string]bool{"Body": true},
		},
		{
			name: "call to an unknown id marks nothing",
			body: `<bpmn:process id="Main">` + call("CA1", `<zeebe:calledElement processId="Elsewhere"/>`) + `</bpmn:process>
<bpmn:process id="Body"/>`,
			want: map[string]bool{},
		},
		{
			name: "call without calledElement marks nothing",
			body: `<bpmn:process id="Main">` + call("CA1", ``) + `</bpmn:process><bpmn:process id="Body"/>`,
			want: map[string]bool{},
		},
		{
			name: "document without calls has an empty map",
			body: `<bpmn:process id="Main"/>`,
			want: map[string]bool{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			defs, err := parse(context.Background(), compOneDoc(tc.body))

			require.NoError(t, err)
			require.NotNil(t, defs.ModuleProcesses)
			assert.Equal(t, tc.want, defs.ModuleProcesses)
		})
	}
}
