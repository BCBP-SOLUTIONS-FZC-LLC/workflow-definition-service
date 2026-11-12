package handler_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

const planQuotaDetail = "the workflow template limit of the plan has been reached; archive a workflow that is no longer used or move to a larger plan"

func TestPlanQuotaRefusal_AnswersWithAFixedSentenceAndTheLimitAndCount(t *testing.T) {
	refusal := fmt.Errorf("create workflow: %w", &domain.PlanQuotaError{Plan: "starter", Limit: 5, Count: 7})
	vid := testVerID.String()
	routes := []struct {
		name   string
		method string
		path   string
		body   any
		h      *handler.Handler
	}{
		{
			name: "create", method: http.MethodPost, path: "/api/v1/workflows",
			body: map[string]any{"key": "k", "name": "n", "bpmn_xml": "<x/>"},
			h: newHandler(&fakeWorkflowSvc{create: func(context.Context, uuid.UUID, uuid.UUID, string, string, string, string) (*domain.Workflow, *domain.WorkflowVersion, error) {
				return nil, nil, refusal
			}}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{}),
		},
		{
			name: "clone", method: http.MethodPost, path: "/api/v1/workflows/" + testWFID.String() + "/versions/" + vid + "/clone",
			body: map[string]any{"new_key": "k2", "new_name": "n2"},
			h: newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{clone: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID, string, service.CloneReq) (*domain.Workflow, *domain.WorkflowVersion, error) {
				return nil, nil, refusal
			}}, &fakeValidationSvc{}),
		},
		{
			name: "promote", method: http.MethodPost, path: "/api/v1/workflows/" + testWFID.String() + "/versions/" + vid + "/promote",
			h: newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{promote: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID, string) (*domain.WorkflowVersion, error) {
				return nil, refusal
			}}, &fakeValidationSvc{}),
		},
	}
	for _, rt := range routes {
		t.Run(rt.name, func(t *testing.T) {
			w := do(newRouter(rt.h), adminReq(rt.method, rt.path, rt.body))

			require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
			problem := decodeProblem(t, w)
			assert.Equal(t, "PLAN_QUOTA_EXCEEDED", problem["code"])
			assert.Equal(t, "https://api.workflow.platform/errors/forbidden", problem["type"])
			assert.Equal(t, "Plan Quota Exceeded", problem["title"])
			assert.Equal(t, planQuotaDetail, problem["detail"])
			assert.Equal(t, "starter", problem["plan_code"])
			assert.EqualValues(t, 5, problem["limit"])
			assert.EqualValues(t, 7, problem["current_count"])
		})
	}
}

func TestPlanQuotaRefusal_ALimitOfZeroIsStillReported(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{
		create: func(context.Context, uuid.UUID, uuid.UUID, string, string, string, string) (*domain.Workflow, *domain.WorkflowVersion, error) {
			return nil, nil, &domain.PlanQuotaError{Plan: "frozen", Limit: 0, Count: 0}
		},
	}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})

	w := do(newRouter(h), adminReq(http.MethodPost, "/api/v1/workflows", map[string]any{"key": "k", "name": "n", "bpmn_xml": "<x/>"}))

	require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	problem := decodeProblem(t, w)
	assert.Equal(t, "frozen", problem["plan_code"])
	assert.EqualValues(t, 0, problem["limit"])
	assert.EqualValues(t, 0, problem["current_count"])
}

func TestPlanQuotaRefusal_WithoutDetailsStillGetsTheFixedSentenceAndNoExtraFields(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{
		create: func(context.Context, uuid.UUID, uuid.UUID, string, string, string, string) (*domain.Workflow, *domain.WorkflowVersion, error) {
			return nil, nil, fmt.Errorf("create workflow: %w", domain.ErrPlanQuotaExceeded)
		},
	}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})

	w := do(newRouter(h), adminReq(http.MethodPost, "/api/v1/workflows", map[string]any{"key": "k", "name": "n", "bpmn_xml": "<x/>"}))

	require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	problem := decodeProblem(t, w)
	assert.Equal(t, planQuotaDetail, problem["detail"])
	assert.NotContains(t, problem, "plan_code")
	assert.NotContains(t, problem, "limit")
	assert.NotContains(t, problem, "current_count")
}
