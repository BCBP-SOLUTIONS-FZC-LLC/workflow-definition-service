package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestValidateBPMN_Valid(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{
		validate: func(_ context.Context, bpmnXML string) (bool, []domain.BPMNValidationError, error) {
			assert.Equal(t, "<definitions/>", bpmnXML)
			return true, nil, nil
		},
	})

	body := map[string]any{"bpmn_xml": "<definitions/>"}
	w := do(newRouter(h), req(http.MethodPost, "/api/v1/workflows/validate", body))
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, true, resp["is_valid"])
	errs := resp["errors"].([]any)
	assert.Empty(t, errs)
}

func TestValidateBPMN_Invalid(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{
		validate: func(_ context.Context, _ string) (bool, []domain.BPMNValidationError, error) {
			return false, []domain.BPMNValidationError{
				{Code: domain.BPMNErrNoStartEvent, NodeID: "node-1", Message: "missing start event"},
				{Code: domain.BPMNErrCycleDetected, NodeID: "node-2", Message: "cycle detected"},
			}, nil
		},
	})

	body := map[string]any{"bpmn_xml": "<bad/>"}
	w := do(newRouter(h), req(http.MethodPost, "/api/v1/workflows/validate", body))
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, false, resp["is_valid"])
	errs := resp["errors"].([]any)
	assert.Len(t, errs, 2)
	first := errs[0].(map[string]any)
	assert.Equal(t, "node-1", first["node_id"])
	assert.Equal(t, string(domain.BPMNErrNoStartEvent), first["code"])
}

func TestValidateBPMN_BindError(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	// missing required bpmn_xml field
	w := do(newRouter(h), req(http.MethodPost, "/api/v1/workflows/validate", map[string]any{}))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	var prob map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&prob))
	assert.Equal(t, "BAD_REQUEST", prob["code"])
}

func TestValidateBPMN_ServiceError(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{
		validate: func(_ context.Context, _ string) (bool, []domain.BPMNValidationError, error) {
			return false, nil, errors.New("parse failure")
		},
	})

	body := map[string]any{"bpmn_xml": "<definitions/>"}
	w := do(newRouter(h), req(http.MethodPost, "/api/v1/workflows/validate", body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestValidateBPMN_MissingCtx(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	body := map[string]any{"bpmn_xml": "<definitions/>"}
	w := do(newBareRouter(h), req(http.MethodPost, "/api/v1/workflows/validate", body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
