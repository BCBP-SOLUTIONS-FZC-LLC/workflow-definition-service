package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestValidateBPMN_Valid(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{
		validate: func(_ context.Context, _ uuid.UUID, bpmnXML string) (bool, []domain.BPMNValidationError, error) {
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
	issues := resp["issues"].([]any)
	assert.Empty(t, issues)
}

func TestValidateBPMN_Invalid(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{
		validate: func(_ context.Context, _ uuid.UUID, _ string) (bool, []domain.BPMNValidationError, error) {
			return false, []domain.BPMNValidationError{
				{Code: domain.BPMNErrNoStartEvent, NodeID: "node-1", Message: "missing start event", Severity: domain.SeverityError},
				{Code: domain.BPMNErrCycleDetected, NodeID: "node-2", Message: "cycle detected", Severity: domain.SeverityError},
			}, nil
		},
	})

	body := map[string]any{"bpmn_xml": "<bad/>"}
	w := do(newRouter(h), req(http.MethodPost, "/api/v1/workflows/validate", body))
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, false, resp["is_valid"])
	issues := resp["issues"].([]any)
	assert.Len(t, issues, 2)
	first := issues[0].(map[string]any)
	assert.Equal(t, "node-1", first["node_id"])
	assert.Equal(t, string(domain.BPMNErrNoStartEvent), first["code"])
	assert.Equal(t, string(domain.SeverityError), first["severity"])
}

func TestValidateBPMN_ValidWithWarnings(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{
		validate: func(_ context.Context, _ uuid.UUID, _ string) (bool, []domain.BPMNValidationError, error) {
			return true, []domain.BPMNValidationError{
				{Code: domain.BPMNWarnUnknownStageType, NodeID: "Task_audit", Message: "unknown stage type", Severity: domain.SeverityWarning},
			}, nil
		},
	})

	body := map[string]any{"bpmn_xml": "<definitions/>"}
	w := do(newRouter(h), req(http.MethodPost, "/api/v1/workflows/validate", body))
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, true, resp["is_valid"])
	issues := resp["issues"].([]any)
	require.Len(t, issues, 1)
	first := issues[0].(map[string]any)
	assert.Equal(t, string(domain.BPMNWarnUnknownStageType), first["code"])
	assert.Equal(t, string(domain.SeverityWarning), first["severity"])
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
		validate: func(_ context.Context, _ uuid.UUID, _ string) (bool, []domain.BPMNValidationError, error) {
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

func TestGetVersionValidation_ReportsStoredVersionIssues(t *testing.T) {
	versionID := uuid.New()
	versions := &fakeVersionSvc{
		get: func(_ context.Context, _, _, gotVersionID uuid.UUID) (*domain.WorkflowVersion, error) {
			assert.Equal(t, versionID, gotVersionID)
			return &domain.WorkflowVersion{
				ID: versionID, Status: domain.VersionStatusPublished,
				BPMNXML: "<definitions/>",
			}, nil
		},
	}
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, versions, &fakeValidationSvc{
		validate: func(_ context.Context, _ uuid.UUID, bpmnXML string) (bool, []domain.BPMNValidationError, error) {
			assert.Equal(t, "<definitions/>", bpmnXML, "the stored version's own BPMN is validated")
			return false, []domain.BPMNValidationError{
				{Code: domain.BPMNErrCandidateUsersEmpty, NodeID: "Activity_1", Message: "no candidateUsers", Severity: domain.SeverityError},
			}, nil
		},
	})

	path := "/api/v1/workflows/" + uuid.New().String() + "/versions/" + versionID.String() + "/validation"
	w := do(newRouter(h), req(http.MethodGet, path, nil))
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, false, resp["is_valid"])
	assert.Equal(t, string(domain.VersionStatusPublished), resp["status"], "a published version keeps its status")
	issues := resp["issues"].([]any)
	require.Len(t, issues, 1)
	assert.Equal(t, string(domain.BPMNErrCandidateUsersEmpty), issues[0].(map[string]any)["code"])
	assert.Equal(t, "Activity_1", issues[0].(map[string]any)["node_id"])
}

func TestGetVersionValidation_UnknownVersion404(t *testing.T) {
	versions := &fakeVersionSvc{
		get: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
			return nil, domain.ErrNotFound
		},
	}
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, versions, &fakeValidationSvc{})

	path := "/api/v1/workflows/" + uuid.New().String() + "/versions/" + uuid.New().String() + "/validation"
	w := do(newRouter(h), req(http.MethodGet, path, nil))
	assert.Equal(t, http.StatusNotFound, w.Code)
}
