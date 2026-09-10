package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func problemCode(t *testing.T, body []byte) string {
	t.Helper()
	var prob map[string]any
	if err := json.Unmarshal(body, &prob); err != nil {
		t.Fatalf("response is not JSON: %v (%s)", err, body)
	}
	code, _ := prob["code"].(string)
	return code
}

func TestValidateBPMN_Malformed_400(t *testing.T) {
	val := &fakeValidationSvc{validate: func(context.Context, string, []string) (bool, []domain.BPMNValidationError, error) {
		return false, nil, fmt.Errorf("validate bpmn: %w: XML decode: unexpected EOF", domain.ErrMalformedBPMN)
	}}
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, val)

	w := do(newRouter(h), req(http.MethodPost, "/api/v1/workflows/validate", map[string]string{"bpmn_xml": "<unclosed>"}))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "INVALID_BPMN_XML", problemCode(t, w.Body.Bytes()))
}

func TestValidateBPMN_ForbiddenXML_400_Generic(t *testing.T) {
	val := &fakeValidationSvc{validate: func(context.Context, string, []string) (bool, []domain.BPMNValidationError, error) {
		return false, nil, fmt.Errorf("validate bpmn: %w: %w: DOCTYPE detected",
			domain.ErrMalformedBPMN, domain.ErrForbiddenXML)
	}}
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, val)

	w := do(newRouter(h), req(http.MethodPost, "/api/v1/workflows/validate", map[string]string{"bpmn_xml": "<!DOCTYPE x>"}))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "INVALID_BPMN_XML", problemCode(t, w.Body.Bytes()))
}

func TestCreateWorkflow_BPMNStatusMapping(t *testing.T) {
	body := map[string]string{"key": "wf", "name": "WF", "bpmn_xml": "<x>"}

	t.Run("malformed -> 400", func(t *testing.T) {
		wf := &fakeWorkflowSvc{create: func(context.Context, uuid.UUID, uuid.UUID, string, string, string, string) (*domain.Workflow, *domain.WorkflowVersion, error) {
			return nil, nil, fmt.Errorf("create workflow: %w", domain.ErrMalformedBPMN)
		}}
		h := newHandler(wf, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
		w := do(newRouter(h), adminReq(http.MethodPost, "/api/v1/workflows", body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Equal(t, "INVALID_BPMN_XML", problemCode(t, w.Body.Bytes()))
	})

	t.Run("parseable but invalid -> 422", func(t *testing.T) {
		wf := &fakeWorkflowSvc{create: func(context.Context, uuid.UUID, uuid.UUID, string, string, string, string) (*domain.Workflow, *domain.WorkflowVersion, error) {
			return nil, nil, &domain.ValidationFailedError{Errors: []domain.BPMNValidationError{
				{Code: domain.BPMNErrNoStartEvent, Message: "no start event"},
			}}
		}}
		h := newHandler(wf, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
		w := do(newRouter(h), adminReq(http.MethodPost, "/api/v1/workflows", body))
		assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
		assert.Equal(t, "BPMN_VALIDATION_FAILED", problemCode(t, w.Body.Bytes()))
	})
}

func TestValidateBPMN_AllErrorsInResponse(t *testing.T) {
	val := &fakeValidationSvc{validate: func(context.Context, string, []string) (bool, []domain.BPMNValidationError, error) {
		return false, []domain.BPMNValidationError{
			{Code: domain.BPMNErrCandidateGroupsEmpty, NodeID: "Task_1", Message: "missing role", Severity: domain.SeverityError},
			{Code: domain.BPMNErrUnmatchedGateway, NodeID: "GW_1", Message: "no matching join", Severity: domain.SeverityError},
		}, nil
	}}
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, val)

	w := do(newRouter(h), req(http.MethodPost, "/api/v1/workflows/validate", map[string]string{"bpmn_xml": "<x/>"}))

	assert.Equal(t, http.StatusOK, w.Code)

	var body struct {
		IsValid bool `json:"is_valid"`
		Issues  []struct {
			NodeID   string `json:"node_id"`
			Code     string `json:"code"`
			Message  string `json:"message"`
			Severity string `json:"severity"`
		} `json:"issues"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.False(t, body.IsValid)

	codes := make([]string, len(body.Issues))
	for i, e := range body.Issues {
		codes[i] = e.Code
	}
	assert.ElementsMatch(t, []string{"CANDIDATE_GROUPS_EMPTY", "UNMATCHED_GATEWAY"}, codes)
}

func TestGetWorkflow_BadUUIDParam_400(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})

	w := do(newRouter(h), req(http.MethodGet, "/api/v1/workflows/not-a-uuid", nil))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "BAD_REQUEST", problemCode(t, w.Body.Bytes()))
}

func TestGetWorkflow_VersionsLimitClamped(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  int
	}{
		{"negative clamps to default", "?versions_limit=-1", 20},
		{"oversized clamps to default", "?versions_limit=99999", 20},
		{"non-numeric clamps to default", "?versions_limit=abc", 20},
		{"in range passes through", "?versions_limit=5", 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got int
			wf := &fakeWorkflowSvc{get: func(_ context.Context, _, _ uuid.UUID, versionsLimit int) (*domain.Workflow, []*domain.WorkflowVersion, error) {
				got = versionsLimit
				return newWorkflow(), nil, nil
			}}
			h := newHandler(wf, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})

			w := do(newRouter(h), req(http.MethodGet, "/api/v1/workflows/"+testWFID.String()+tt.query, nil))

			assert.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, tt.want, got)
		})
	}
}
