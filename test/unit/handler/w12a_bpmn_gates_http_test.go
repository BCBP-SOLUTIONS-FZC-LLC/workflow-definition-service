package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

const (
	w12aProblemBase    = "https://api.workflow.platform/errors/"
	w12aCreatePath     = "/api/v1/workflows"
	w12aValidatePath   = "/api/v1/workflows/validate"
	w12aNSBPMN         = "http://www.omg.org/spec/BPMN/20100524/MODEL"
	w12aNSZeebe        = "http://camunda.org/schema/zeebe/1.0"
	w12aTokenLimit     = 1_000_000
	w12aForbiddenWarn  = "forbidden XML construct rejected"
	w12aUnparsableText = "the BPMN document could not be parsed"
	w12aUserA          = "018f2d3c-0000-7000-8000-0000000000a1"
)

type w12aLogLine struct {
	msg    string
	fields map[string]any
}

type w12aLog struct {
	mu    sync.Mutex
	warns []w12aLogLine
}

func (l *w12aLog) Debug(string, map[string]any) { /* no-op */ }
func (l *w12aLog) Info(string, map[string]any)  { /* no-op */ }
func (l *w12aLog) Error(string, map[string]any) { /* no-op */ }
func (l *w12aLog) Fatal(string, map[string]any) { /* no-op */ }
func (l *w12aLog) Warn(msg string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warns = append(l.warns, w12aLogLine{msg: msg, fields: fields})
}

func (l *w12aLog) forbiddenWarnings() []w12aLogLine {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []w12aLogLine
	for _, line := range l.warns {
		if line.msg == w12aForbiddenWarn {
			out = append(out, line)
		}
	}
	return out
}

type w12aRig struct {
	router    *gin.Engine
	log       *w12aLog
	modules   *mocks.MockModuleVersionRepository
	workflows *mocks.MockWorkflowRepository
	versions  *mocks.MockWorkflowVersionRepository
}

func w12aNewRig(t *testing.T) *w12aRig {
	t.Helper()
	ctrl := gomock.NewController(t)
	rig := &w12aRig{
		log:       &w12aLog{},
		modules:   mocks.NewMockModuleVersionRepository(ctrl),
		workflows: mocks.NewMockWorkflowRepository(ctrl),
		versions:  mocks.NewMockWorkflowVersionRepository(ctrl),
	}
	transactor := mocks.NewMockTransactor(ctrl)
	compiler := bpmn_compiler.New()
	rig.router = newRouter(handler.New(handler.Services{
		Workflows: service.NewWorkflowService(service.WorkflowDeps{
			Transactor: transactor, Workflows: rig.workflows, Versions: rig.versions,
			ModuleVersions: rig.modules, Compiler: compiler,
		}),
		Versions: service.NewVersionService(service.VersionDeps{
			Transactor: transactor, Workflows: rig.workflows, Versions: rig.versions,
			ModuleVersions: rig.modules, Compiler: compiler,
		}),
		Validation: service.NewValidationService(service.ValidationDeps{Compiler: compiler, ModuleVersions: rig.modules}),
		Log:        rig.log,
	}))
	return rig
}

func w12aCreate(rig *w12aRig, bpmnXML string) *httptest.ResponseRecorder {
	return do(rig.router, adminReq(http.MethodPost, w12aCreatePath, map[string]any{
		"key": "w12a-key", "name": "W12a", "bpmn_xml": bpmnXML,
	}))
}

func w12aValidate(rig *w12aRig, bpmnXML string) *httptest.ResponseRecorder {
	return do(rig.router, req(http.MethodPost, w12aValidatePath, map[string]any{"bpmn_xml": bpmnXML}))
}

func w12aPublishPath() string {
	return "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/publish"
}

func w12aPublish(t *testing.T, rig *w12aRig, bpmnXML string) *httptest.ResponseRecorder {
	t.Helper()
	rig.versions.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).Return(&domain.WorkflowVersion{
		ID: testVerID, WorkflowID: testWFID, TenantID: testTenantID,
		Status: domain.VersionStatusDraft, BPMNXML: bpmnXML, RecordVersion: 1,
	}, nil)
	return do(rig.router, adminReq(http.MethodPost, w12aPublishPath(), nil))
}

func w12aProblem(t *testing.T, w *httptest.ResponseRecorder, status int, typeSuffix, code, instance string) map[string]any {
	t.Helper()
	require.Equal(t, status, w.Code, w.Body.String())
	assert.Equal(t, "application/problem+json", strings.Split(w.Header().Get("Content-Type"), ";")[0])
	prob := decodeProblem(t, w)
	assert.Equal(t, w12aProblemBase+typeSuffix, prob["type"])
	assert.Equal(t, code, prob["code"])
	assert.EqualValues(t, status, prob["status"])
	assert.Equal(t, instance, prob["instance"])
	return prob
}

func w12aInvalidParams(t *testing.T, prob map[string]any) []map[string]any {
	t.Helper()
	raw, ok := prob["invalid_params"].([]any)
	require.True(t, ok, "problem body carries invalid_params: %v", prob)
	out := make([]map[string]any, len(raw))
	for i, p := range raw {
		out[i], ok = p.(map[string]any)
		require.True(t, ok)
	}
	return out
}

type w12aIssue struct {
	NodeID   string `json:"node_id"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
}

func w12aValidateBody(t *testing.T, w *httptest.ResponseRecorder) (isValid bool, issues []w12aIssue) {
	t.Helper()
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var body struct {
		IsValid bool        `json:"is_valid"`
		Issues  []w12aIssue `json:"issues"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return body.IsValid, body.Issues
}

func w12aTokenDocument(tokens int) string {
	if tokens%2 == 0 {
		return "<r>" + strings.Repeat("<a/>", (tokens-2)/2) + "</r>"
	}
	return "<r>x" + strings.Repeat("<a/>", (tokens-3)/2) + "</r>"
}

func w12aCallerDocument(calledElementAttrs string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="` + w12aNSBPMN + `" xmlns:zeebe="` + w12aNSZeebe + `" id="Def_Caller" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="Process_Main" isExecutable="true">
    <bpmn:startEvent id="Start_1"/>
    <bpmn:callActivity id="CA_1" name="Call lib"><bpmn:extensionElements><zeebe:calledElement ` + calledElementAttrs + `/></bpmn:extensionElements></bpmn:callActivity>
    <bpmn:endEvent id="End_1"/>
    <bpmn:sequenceFlow id="F1" sourceRef="Start_1" targetRef="CA_1"/>
    <bpmn:sequenceFlow id="F2" sourceRef="CA_1" targetRef="End_1"/>
  </bpmn:process>
</bpmn:definitions>`
}

type w12aBadCall struct {
	name       string
	attrs      string
	wantReason string
}

func w12aBadCalls() []w12aBadCall {
	return []w12aBadCall{
		{"expression process id", `processId="= &quot;Process_&quot; + kind"`, "names its module with an expression"},
		{"version tag that is not a number", `processId="Process_Lib" bindingType="versionTag" versionTag="v3"`, `pins versionTag "v3"`},
		{"version tag missing", `processId="Process_Lib" bindingType="versionTag"`, `pins versionTag ""`},
		{"version tag zero", `processId="Process_Lib" bindingType="versionTag" versionTag="0"`, `pins versionTag "0"`},
		{"deployment binding for a process not in the file", `processId="Process_Lib" bindingType="deployment"`, "uses bindingType deployment"},
		{"unknown binding type", `processId="Process_Lib" bindingType="zzq_unknown"`, `uses bindingType "zzq_unknown"`},
	}
}

func TestW12aCreateWorkflow_CallNamingItsModuleBadly_Returns422OnTheCallWithoutAnyLookup(t *testing.T) {
	t.Parallel()
	for _, tc := range w12aBadCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rig := w12aNewRig(t)

			w := w12aCreate(rig, w12aCallerDocument(tc.attrs))

			prob := w12aProblem(t, w, http.StatusUnprocessableEntity, "validation-failed", "BPMN_VALIDATION_FAILED", w12aCreatePath)
			assert.Equal(t, "BPMN semantic or structural validation failed.", prob["detail"])
			params := w12aInvalidParams(t, prob)
			require.Len(t, params, 1)
			assert.Equal(t, "CA_1", params[0]["name"])
			assert.Equal(t, "UNRESOLVED_CALLED_ELEMENT", params[0]["code"])
			assert.Contains(t, params[0]["reason"], tc.wantReason)
		})
	}
}

func TestW12aValidateBPMN_CallNamingItsModuleBadly_Returns200InvalidWithoutAnyLookup(t *testing.T) {
	t.Parallel()
	for _, tc := range w12aBadCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rig := w12aNewRig(t)

			w := w12aValidate(rig, w12aCallerDocument(tc.attrs))

			isValid, issues := w12aValidateBody(t, w)
			assert.False(t, isValid)
			require.Len(t, issues, 1)
			assert.Equal(t, "UNRESOLVED_CALLED_ELEMENT", issues[0].Code)
			assert.Equal(t, "CA_1", issues[0].NodeID)
			assert.Equal(t, "error", issues[0].Severity)
			assert.Contains(t, issues[0].Message, tc.wantReason)
		})
	}
}

func TestW12aPublishVersion_DraftCallNamingItsModuleBadly_Returns422OnTheCallAndPublishesNothing(t *testing.T) {
	t.Parallel()
	for _, tc := range w12aBadCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rig := w12aNewRig(t)

			w := w12aPublish(t, rig, w12aCallerDocument(tc.attrs))

			prob := w12aProblem(t, w, http.StatusUnprocessableEntity, "validation-failed", "BPMN_VALIDATION_FAILED", w12aPublishPath())
			params := w12aInvalidParams(t, prob)
			require.Len(t, params, 1)
			assert.Equal(t, "CA_1", params[0]["name"])
			assert.Equal(t, "UNRESOLVED_CALLED_ELEMENT", params[0]["code"])
			assert.Contains(t, params[0]["reason"], tc.wantReason)
		})
	}
}

func TestW12aValidateBPMN_BadCallAlongsideAGoodCallAndARejectedElement_ReturnsBothIssues(t *testing.T) {
	t.Parallel()
	rig := w12aNewRig(t)
	number := int32(2)
	rig.modules.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Good", int32(0)).Return(&domain.ModuleVersion{
		ID: uuid.New(), ModuleID: uuid.New(), ProcessID: "Process_Good", VersionNumber: &number,
		Status: domain.VersionStatusPublished, BPMNXML: "<bpmn:definitions xmlns:bpmn=\"" + w12aNSBPMN + "\"/>",
	}, nil)
	doc := strings.Replace(w12aCallerDocument(`processId="Process_Lib" bindingType="zzq_unknown"`),
		`<bpmn:endEvent id="End_1"/>`,
		`<bpmn:callActivity id="CA_2"><bpmn:extensionElements><zeebe:calledElement processId="Process_Good"/></bpmn:extensionElements></bpmn:callActivity>
    <bpmn:scriptTask id="Script_1"/>
    <bpmn:endEvent id="End_1"/>`, 1)

	w := w12aValidate(rig, doc)

	isValid, issues := w12aValidateBody(t, w)
	assert.False(t, isValid)
	require.Len(t, issues, 2)
	assert.Equal(t, "UNRESOLVED_CALLED_ELEMENT", issues[0].Code)
	assert.Equal(t, "CA_1", issues[0].NodeID)
	assert.Equal(t, "REJECTED_ELEMENT", issues[1].Code)
	assert.Equal(t, "Script_1", issues[1].NodeID)
}

func TestW12aPublishVersion_DraftWithUnparsableXML_Returns400InvalidBPMNXML(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		xml  string
	}{
		{"element never closed", `<bpmn:definitions xmlns:bpmn="` + w12aNSBPMN + `"><bpmn:process id="P1"`},
		{"end tag that closes another element", `<a><b></a>`},
		{"attribute without a value", `<bpmn:definitions xmlns:bpmn="` + w12aNSBPMN + `" id>`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rig := w12aNewRig(t)

			w := w12aPublish(t, rig, tc.xml)

			prob := w12aProblem(t, w, http.StatusBadRequest, "bad-request", "INVALID_BPMN_XML", w12aPublishPath())
			assert.Equal(t, w12aUnparsableText, prob["detail"])
			assert.NotContains(t, prob, "invalid_params")
		})
	}
}

func TestW12aCreateWorkflow_ForbiddenXML_Returns400InvalidBPMNXMLAndLogsOneWarning(t *testing.T) {
	t.Parallel()
	const marker = "zzq_file_marker"
	prologue := `<?xml version="1.0"?>`
	cases := []struct {
		name string
		xml  string
	}{
		{"document type declaration with an external entity", prologue + `<!DOCTYPE zzq_root [<!ENTITY zzq SYSTEM "file:///` + marker + `">]><zzq_root/>`},
		{"document type declaration in lower case", prologue + `<!doctype zzq_root SYSTEM "file:///` + marker + `"><zzq_root/>`},
		{"entity declaration inside a BPMN document", `<bpmn:definitions xmlns:bpmn="` + w12aNSBPMN + `" xmlns:zeebe="` + w12aNSZeebe + `"><!ENTITY zzq "file:///` + marker + `"></bpmn:definitions>`},
		{"more than a million tokens", w12aTokenDocument(w12aTokenLimit + 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rig := w12aNewRig(t)

			w := w12aCreate(rig, tc.xml)

			prob := w12aProblem(t, w, http.StatusBadRequest, "bad-request", "INVALID_BPMN_XML", w12aCreatePath)
			assert.Equal(t, w12aUnparsableText, prob["detail"])
			assert.NotContains(t, w.Body.String(), marker)
			warnings := rig.log.forbiddenWarnings()
			require.Len(t, warnings, 1)
			assert.Equal(t, testTenantID.String(), warnings[0].fields["tenant_id"])
			errText, _ := warnings[0].fields["error"].(string)
			assert.NotEmpty(t, errText)
			assert.NotContains(t, errText, marker)
		})
	}
}

func TestW12aValidateBPMN_TokenLimitBoundary_OneMillionIsScannedOneMillionAndOneIsForbidden(t *testing.T) {
	t.Parallel()
	t.Run("a million tokens pass the scan and are judged on their content", func(t *testing.T) {
		t.Parallel()
		rig := w12aNewRig(t)

		w := w12aValidate(rig, w12aTokenDocument(w12aTokenLimit))

		isValid, issues := w12aValidateBody(t, w)
		assert.False(t, isValid)
		require.Len(t, issues, 1)
		assert.Equal(t, "MISSING_NAMESPACE", issues[0].Code)
		assert.Empty(t, rig.log.forbiddenWarnings())
	})
	t.Run("a million and one tokens are refused as forbidden XML", func(t *testing.T) {
		t.Parallel()
		rig := w12aNewRig(t)

		w := w12aValidate(rig, w12aTokenDocument(w12aTokenLimit+1))

		prob := w12aProblem(t, w, http.StatusBadRequest, "bad-request", "INVALID_BPMN_XML", w12aValidatePath)
		assert.Equal(t, w12aUnparsableText, prob["detail"])
		warnings := rig.log.forbiddenWarnings()
		require.Len(t, warnings, 1)
		assert.Equal(t, testTenantID.String(), warnings[0].fields["tenant_id"])
	})
}

func w12aNamespaceCases() []struct {
	name       string
	xml        string
	wantNamed  string
	wantAbsent string
} {
	return []struct {
		name       string
		xml        string
		wantNamed  string
		wantAbsent string
	}{
		{"plain text", "this is plain text", w12aNSBPMN, w12aNSZeebe},
		{"BPMN namespace not declared", `<definitions xmlns:zeebe="` + w12aNSZeebe + `" id="D1"><process id="P1"/></definitions>`, w12aNSBPMN, w12aNSZeebe},
		{"Zeebe namespace not declared", `<bpmn:definitions xmlns:bpmn="` + w12aNSBPMN + `" id="D1"><bpmn:process id="P1"/></bpmn:definitions>`, w12aNSZeebe, w12aNSBPMN},
		{"BPMN namespace only in a comment and an attribute value", `<!-- ` + w12aNSBPMN + ` --><definitions xmlns:zeebe="` + w12aNSZeebe + `" targetNamespace="` + w12aNSBPMN + `" id="D1"/>`, w12aNSBPMN, w12aNSZeebe},
		{"Zeebe namespace only in a comment", `<bpmn:definitions xmlns:bpmn="` + w12aNSBPMN + `" id="D1"><!-- ` + w12aNSZeebe + ` --></bpmn:definitions>`, w12aNSZeebe, w12aNSBPMN},
	}
}

func TestW12aValidateBPMN_NamespaceNotDeclared_Returns200InvalidNamingTheMissingNamespace(t *testing.T) {
	t.Parallel()
	for _, tc := range w12aNamespaceCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rig := w12aNewRig(t)

			w := w12aValidate(rig, tc.xml)

			isValid, issues := w12aValidateBody(t, w)
			assert.False(t, isValid)
			require.Len(t, issues, 1)
			assert.Equal(t, "MISSING_NAMESPACE", issues[0].Code)
			assert.Equal(t, "error", issues[0].Severity)
			assert.Empty(t, issues[0].NodeID)
			assert.Contains(t, issues[0].Message, tc.wantNamed)
			assert.NotContains(t, issues[0].Message, tc.wantAbsent)
		})
	}
}

func TestW12aCreateWorkflow_NamespaceNotDeclared_Returns422MissingNamespaceAndStoresNothing(t *testing.T) {
	t.Parallel()
	for _, tc := range w12aNamespaceCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rig := w12aNewRig(t)

			w := w12aCreate(rig, tc.xml)

			prob := w12aProblem(t, w, http.StatusUnprocessableEntity, "validation-failed", "BPMN_VALIDATION_FAILED", w12aCreatePath)
			assert.Equal(t, "BPMN semantic or structural validation failed.", prob["detail"])
			params := w12aInvalidParams(t, prob)
			require.Len(t, params, 1)
			assert.Equal(t, "MISSING_NAMESPACE", params[0]["code"])
			assert.Equal(t, "", params[0]["name"])
			reason, _ := params[0]["reason"].(string)
			assert.Contains(t, reason, tc.wantNamed)
			assert.NotContains(t, reason, tc.wantAbsent)
		})
	}
}

func w12aLanelessDocument(taskXML, taskID string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="` + w12aNSBPMN + `" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" xmlns:zeebe="` + w12aNSZeebe + `" id="Def_L" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:message id="Msg_n" name="zzq_message"/>
  <bpmn:process id="Process_Main" name="Main" isExecutable="true">
    <bpmn:startEvent id="Start_1"/>
    ` + taskXML + `
    <bpmn:endEvent id="End_1"/>
    <bpmn:sequenceFlow id="F1" sourceRef="Start_1" targetRef="` + taskID + `"/>
    <bpmn:sequenceFlow id="F2" sourceRef="` + taskID + `" targetRef="End_1"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="D"><bpmndi:BPMNPlane id="P" bpmnElement="Process_Main">
    <bpmndi:BPMNShape id="Start_1_di" bpmnElement="Start_1"/>
    <bpmndi:BPMNShape id="` + taskID + `_di" bpmnElement="` + taskID + `"/>
    <bpmndi:BPMNShape id="End_1_di" bpmnElement="End_1"/>
    <bpmndi:BPMNEdge id="F1_di" bpmnElement="F1"/>
    <bpmndi:BPMNEdge id="F2_di" bpmnElement="F2"/>
  </bpmndi:BPMNPlane></bpmndi:BPMNDiagram>
</bpmn:definitions>`
}

func w12aUserTaskXML(assignment string) string {
	return `<bpmn:userTask id="Task_A"><bpmn:extensionElements><zeebe:taskDefinition type="prep"/><zeebe:assignmentDefinition ` + assignment + `/></bpmn:extensionElements></bpmn:userTask>`
}

func w12aSendTaskXML(assignment string) string {
	return `<bpmn:sendTask id="Task_A" messageRef="Msg_n"><bpmn:extensionElements><zeebe:taskDefinition type="send"/><zeebe:assignmentDefinition ` + assignment + `/></bpmn:extensionElements></bpmn:sendTask>`
}

func TestW12aValidateBPMN_WorkWithNoLanes_Returns200InvalidWithThePublishRefusalAndNoWarnings(t *testing.T) {
	t.Parallel()
	named := `candidateGroups="preparator" candidateUsers="` + w12aUserA + `"`
	unnamed := `candidateGroups="preparator"`
	cases := []struct {
		name string
		xml  string
	}{
		{"user task naming a user", w12aLanelessDocument(w12aUserTaskXML(named), "Task_A")},
		{"user task naming no user", w12aLanelessDocument(w12aUserTaskXML(unnamed), "Task_A")},
		{"send task naming a user", w12aLanelessDocument(w12aSendTaskXML(named), "Task_A")},
		{"send task naming no user", w12aLanelessDocument(w12aSendTaskXML(unnamed), "Task_A")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rig := w12aNewRig(t)

			w := w12aValidate(rig, tc.xml)

			isValid, issues := w12aValidateBody(t, w)
			assert.False(t, isValid)
			require.Len(t, issues, 1, "the non-blocking candidate-users warning is dropped, only the publish refusal remains")
			assert.Equal(t, "MISSING_DEPT_ID", issues[0].Code)
			assert.Equal(t, "Task_A", issues[0].NodeID)
			assert.Equal(t, "error", issues[0].Severity)
			assert.Contains(t, issues[0].Message, "has no lanes")
		})
	}
}
