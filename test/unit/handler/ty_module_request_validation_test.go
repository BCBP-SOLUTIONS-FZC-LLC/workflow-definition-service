package handler_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type tyInvalidParam struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

func tyInvalidParams(t *testing.T, prob map[string]any) []tyInvalidParam {
	t.Helper()
	raw, ok := prob["invalid_params"].([]any)
	require.True(t, ok, "problem body carries invalid_params: %v", prob)
	out := make([]tyInvalidParam, len(raw))
	for i, p := range raw {
		entry, ok := p.(map[string]any)
		require.True(t, ok)
		out[i] = tyInvalidParam{Name: entry["name"].(string), Reason: entry["reason"].(string)}
	}
	return out
}

func TestListModules_BadPageOrLimit_Returns400NamingTheParameterAndNeverCallsTheService(t *testing.T) {
	t.Parallel()

	const wantReasonPage = "must be an integer between 1 and 100000"
	const wantReasonLimit = "must be an integer between 1 and 100"
	cases := []struct {
		name       string
		query      string
		wantName   string
		wantReason string
	}{
		{"page zero", "?page=0", "page", wantReasonPage},
		{"page negative", "?page=-1", "page", wantReasonPage},
		{"page not a number", "?page=abc", "page", wantReasonPage},
		{"page above the maximum", "?page=100001", "page", wantReasonPage},
		{"page empty", "?page=", "page", wantReasonPage},
		{"limit zero", "?limit=0", "limit", wantReasonLimit},
		{"limit negative", "?limit=-1", "limit", wantReasonLimit},
		{"limit not a number", "?limit=abc", "limit", wantReasonLimit},
		{"limit above the maximum", "?limit=101", "limit", wantReasonLimit},
		{"limit empty", "?limit=", "limit", wantReasonLimit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			h := newModuleHandler(&fakeModuleSvc{
				list: func(context.Context, uuid.UUID, port.ModuleFilter) ([]*domain.Module, int64, error) {
					calls++
					return nil, 0, nil
				},
			})

			w := do(newRouter(h), req(http.MethodGet, "/api/v1/modules"+tc.query, nil))

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, "INVALID_INPUT", prob["code"])
			assert.Equal(t, []tyInvalidParam{{Name: tc.wantName, Reason: tc.wantReason}}, tyInvalidParams(t, prob))
			assert.Zero(t, calls, "the service must not be called")
		})
	}
}

func TestCreateModule_RequiredFieldMissingNullOrEmpty_Returns400NamingTheJSONField(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		body       map[string]any
		wantParams []tyInvalidParam
	}{
		{"name absent", map[string]any{"bpmn_xml": "<bpmn/>"}, []tyInvalidParam{{"name", "is required"}}},
		{"name null", map[string]any{"name": nil, "bpmn_xml": "<bpmn/>"}, []tyInvalidParam{{"name", "is required"}}},
		{"name empty", map[string]any{"name": "", "bpmn_xml": "<bpmn/>"}, []tyInvalidParam{{"name", "is required"}}},
		{"bpmn_xml absent", map[string]any{"name": "Review"}, []tyInvalidParam{{"bpmn_xml", "is required"}}},
		{"bpmn_xml null", map[string]any{"name": "Review", "bpmn_xml": nil}, []tyInvalidParam{{"bpmn_xml", "is required"}}},
		{"bpmn_xml empty", map[string]any{"name": "Review", "bpmn_xml": ""}, []tyInvalidParam{{"bpmn_xml", "is required"}}},
		{
			name:       "both absent are listed in field order",
			body:       map[string]any{"description": "only a description"},
			wantParams: []tyInvalidParam{{"name", "is required"}, {"bpmn_xml", "is required"}},
		},
		{
			name:       "both empty are listed in field order",
			body:       map[string]any{"name": "", "bpmn_xml": ""},
			wantParams: []tyInvalidParam{{"name", "is required"}, {"bpmn_xml", "is required"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			h := newModuleHandler(&fakeModuleSvc{
				create: func(context.Context, uuid.UUID, uuid.UUID, service.CreateModuleReq) (*domain.Module, *domain.ModuleVersion, error) {
					calls++
					return nil, nil, nil
				},
			})

			w := do(newRouter(h), adminReq(http.MethodPost, "/api/v1/modules", tc.body))

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, "BAD_REQUEST", prob["code"])
			assert.Equal(t, tc.wantParams, tyInvalidParams(t, prob))
			assert.Zero(t, calls, "the service must not be called")
		})
	}
}
