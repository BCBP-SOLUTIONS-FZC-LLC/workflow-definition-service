package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

const w8cValidatePath = "/api/v1/workflows/validate"

type w8cValidateCall struct {
	calls  int
	tenant uuid.UUID
	bpmn   string
}

func w8cValidateHandlerSpy(spy *w8cValidateCall, isValid bool, issues []domain.BPMNValidationError) *fakeValidationSvc {
	return &fakeValidationSvc{
		validate: func(_ context.Context, tenantID uuid.UUID, bpmnXML string) (bool, []domain.BPMNValidationError, error) {
			spy.calls++
			spy.tenant, spy.bpmn = tenantID, bpmnXML
			return isValid, issues, nil
		},
	}
}

func TestValidateBPMN_NoIdentityInContext_Returns500InternalProblemWithoutValidating(t *testing.T) {
	spy := &w8cValidateCall{}
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, w8cValidateHandlerSpy(spy, true, nil))

	w := do(newBareRouter(h), req(http.MethodPost, w8cValidatePath, map[string]any{"bpmn_xml": "<definitions/>"}))

	prob := w8cAssertProblem(t, w, http.StatusInternalServerError, w8cTypeInternal, "INTERNAL_ERROR", "missing request context", w8cValidatePath)
	assert.Equal(t, "Internal Server Error", prob["title"])
	assert.Zero(t, spy.calls)
}

func TestValidateBPMN_SeveralIssues_ReturnsOneEntryPerIssueInOrderWithAllFourFields(t *testing.T) {
	issues := []domain.BPMNValidationError{
		{Code: domain.BPMNErrMissingNamespace, NodeID: "", Message: "zzq namespace message", Severity: domain.SeverityError},
		{Code: domain.BPMNErrUnmatchedGateway, NodeID: "GW_1", Message: "zzq gateway message", Severity: domain.SeverityError},
		{Code: domain.BPMNWarnUnknownStageType, NodeID: "Task_7", Message: "zzq stage message", Severity: domain.SeverityWarning},
		{Code: domain.BPMNErrNoStartEvent, NodeID: "Process_1", Message: "zzq start message", Severity: domain.SeverityError},
	}
	spy := &w8cValidateCall{}
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, w8cValidateHandlerSpy(spy, false, issues))

	w := do(newRouter(h), req(http.MethodPost, w8cValidatePath, map[string]any{"bpmn_xml": "<zzq-document/>"}))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var body struct {
		IsValid bool             `json:"is_valid"`
		Issues  []map[string]any `json:"issues"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.False(t, body.IsValid)
	want := []map[string]any{
		{"node_id": "", "code": string(domain.BPMNErrMissingNamespace), "message": "zzq namespace message", "severity": string(domain.SeverityError)},
		{"node_id": "GW_1", "code": string(domain.BPMNErrUnmatchedGateway), "message": "zzq gateway message", "severity": string(domain.SeverityError)},
		{"node_id": "Task_7", "code": string(domain.BPMNWarnUnknownStageType), "message": "zzq stage message", "severity": string(domain.SeverityWarning)},
		{"node_id": "Process_1", "code": string(domain.BPMNErrNoStartEvent), "message": "zzq start message", "severity": string(domain.SeverityError)},
	}
	assert.Equal(t, want, body.Issues)
	for i, issue := range body.Issues {
		assert.Len(t, issue, 4, "issue %d has exactly node_id, code, message and severity", i)
	}
	assert.Equal(t, testTenantID, spy.tenant, "the caller's tenant is the one validated for")
	assert.Equal(t, "<zzq-document/>", spy.bpmn)
}

func TestValidateBPMN_UnknownFieldInBody_Returns400NamingTheFieldWithoutValidating(t *testing.T) {
	longName := strings.Repeat("q", 100)
	cases := []struct {
		name     string
		body     string
		wantName string
	}{
		{"one unknown field", `{"bpmn_xml":"<valid/>","extra":1}`, "extra"},
		{"unknown field whose name is over 64 characters", `{"bpmn_xml":"<valid/>","` + longName + `":1}`, longName[:64]},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spy := &w8cValidateCall{}
			h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, w8cValidateHandlerSpy(spy, true, nil))

			w := do(newRouter(h), w8cRawReq(http.MethodPost, w8cValidatePath, tc.body, ""))

			prob := w8cAssertProblem(t, w, http.StatusBadRequest, w8cTypeBadRequest, "BAD_REQUEST",
				"request body has fields this endpoint does not define", w8cValidatePath)
			params, _ := prob["invalid_params"].([]any)
			require.Len(t, params, 1)
			param := params[0].(map[string]any)
			assert.Equal(t, tc.wantName, param["name"])
			assert.Equal(t, "is not a known field", param["reason"])
			assert.Zero(t, spy.calls, "a body with an unknown field is refused before any validation work")
		})
	}
}
