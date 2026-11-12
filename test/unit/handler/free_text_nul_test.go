package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type nulRoute struct {
	name   string
	method string
	path   string
	fields []string
	body   map[string]any
}

func nulRoutes() []nulRoute {
	wf, ver := testWFID.String(), testVerID.String()
	return []nulRoute{
		{
			name: "create workflow", method: http.MethodPost, path: "/api/v1/workflows",
			fields: []string{"key", "name", "description"},
			body:   map[string]any{"key": "k", "name": "n", "description": "d", "bpmn_xml": "<definitions/>"},
		},
		{
			name: "save draft", method: http.MethodPut, path: "/api/v1/workflows/" + wf + "/draft",
			fields: []string{"name", "description"},
			body:   map[string]any{"name": "n", "description": "d", "record_version": 1},
		},
		{
			name: "clone version", method: http.MethodPost, path: "/api/v1/workflows/" + wf + "/versions/" + ver + "/clone",
			fields: []string{"new_key", "new_name", "new_description"},
			body:   map[string]any{"new_key": "k", "new_name": "n", "new_description": "d"},
		},
		{
			name: "create module", method: http.MethodPost, path: "/api/v1/modules",
			fields: []string{"name", "description"},
			body:   map[string]any{"name": "n", "description": "d", "bpmn_xml": "<definitions/>"},
		},
		{
			name: "create starter", method: http.MethodPost, path: "/api/v1/starters",
			fields: []string{"name", "description", "category"},
			body:   map[string]any{"name": "n", "description": "d", "category": "c", "bpmn_xml": "<definitions/>"},
		},
		{
			name: "create starter from a version", method: http.MethodPost, path: "/api/v1/starters/from-workflow-version/" + ver,
			fields: []string{"name", "description", "category"},
			body:   map[string]any{"name": "n", "description": "d", "category": "c"},
		},
	}
}

func newNulRouterEngine(reached *bool) *gin.Engine {
	touch := func() { *reached = true }
	return newRouter(handler.New(handler.Services{
		Workflows: &fakeWorkflowSvc{
			create: func(context.Context, uuid.UUID, uuid.UUID, string, string, string, string) (*domain.Workflow, *domain.WorkflowVersion, error) {
				touch()
				return nil, nil, domain.ErrUpstreamUnavailable
			},
		},
		Drafts: &fakeDraftSvc{
			update: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, service.UpdateDraftReq) (*domain.WorkflowVersion, error) {
				touch()
				return nil, domain.ErrUpstreamUnavailable
			},
		},
		Versions: &fakeVersionSvc{
			clone: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID, string, service.CloneReq) (*domain.Workflow, *domain.WorkflowVersion, error) {
				touch()
				return nil, nil, domain.ErrUpstreamUnavailable
			},
		},
		Modules: &fakeModuleSvc{
			create: func(context.Context, uuid.UUID, uuid.UUID, service.CreateModuleReq) (*domain.Module, *domain.ModuleVersion, error) {
				touch()
				return nil, nil, domain.ErrUpstreamUnavailable
			},
		},
		Starters: &fakeStarterSvc{
			create: func(context.Context, uuid.UUID, uuid.UUID, service.CreateStarterReq) (*domain.StarterTemplate, error) {
				touch()
				return nil, domain.ErrUpstreamUnavailable
			},
			createFromWorkflowVersion: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, service.CreateFromWorkflowVersionReq) (*domain.StarterTemplate, error) {
				touch()
				return nil, domain.ErrUpstreamUnavailable
			},
		},
	}))
}

func withField(body map[string]any, field string, value any) map[string]any {
	out := make(map[string]any, len(body))
	for k, v := range body {
		out[k] = v
	}
	out[field] = value
	return out
}

type nulProblem struct {
	Code          string `json:"code"`
	Detail        string `json:"detail"`
	InvalidParams []struct {
		Name   string `json:"name"`
		Reason string `json:"reason"`
	} `json:"invalid_params"`
}

func decodeNulProblem(t *testing.T, body []byte) nulProblem {
	t.Helper()
	var p nulProblem
	require.NoError(t, json.Unmarshal(body, &p))
	return p
}

func TestFreeTextFields_NulCharacter_Returns422BeforeTheServiceRuns(t *testing.T) {
	t.Parallel()

	for _, route := range nulRoutes() {
		for _, field := range route.fields {
			for _, value := range []string{"a\x00b", "\x00", "tail\x00"} {
				t.Run(route.name+"/"+field+"/"+strings.ReplaceAll(value, "\x00", "NUL"), func(t *testing.T) {
					t.Parallel()

					var reached bool
					w := do(newNulRouterEngine(&reached), platformOperatorReq(route.method, route.path, withField(route.body, field, value)))

					require.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
					assert.False(t, reached, "a NUL character must not reach the service")
					p := decodeNulProblem(t, w.Body.Bytes())
					assert.Equal(t, "INVALID_INPUT", p.Code)
					assert.Equal(t, "request fields must not contain NUL characters", p.Detail)
					require.Len(t, p.InvalidParams, 1)
					assert.Equal(t, field, p.InvalidParams[0].Name)
					assert.Equal(t, "must not contain NUL characters", p.InvalidParams[0].Reason)
				})
			}
		}
	}
}

func TestFreeTextFields_WithoutNul_ReachTheService(t *testing.T) {
	t.Parallel()

	for _, route := range nulRoutes() {
		t.Run(route.name, func(t *testing.T) {
			t.Parallel()

			var reached bool
			w := do(newNulRouterEngine(&reached), platformOperatorReq(route.method, route.path, route.body))

			assert.True(t, reached, w.Body.String())
			assert.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
		})
	}
}

func TestFreeTextFields_NulAndTooLong_AreReportedTogether(t *testing.T) {
	t.Parallel()

	var reached bool
	body := withField(withField(nulRoutes()[0].body, "key", "k\x00"), "name", strings.Repeat("n", 256))

	w := do(newNulRouterEngine(&reached), platformOperatorReq(http.MethodPost, "/api/v1/workflows", body))

	require.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
	assert.False(t, reached)
	p := decodeNulProblem(t, w.Body.Bytes())
	assert.Equal(t, "INVALID_INPUT", p.Code)
	assert.Equal(t, "request fields exceed maximum length or contain NUL characters", p.Detail)
	require.Len(t, p.InvalidParams, 2)
	assert.Equal(t, "key", p.InvalidParams[0].Name)
	assert.Equal(t, "must not contain NUL characters", p.InvalidParams[0].Reason)
	assert.Equal(t, "name", p.InvalidParams[1].Name)
	assert.Equal(t, "must not exceed 255 characters", p.InvalidParams[1].Reason)
}
