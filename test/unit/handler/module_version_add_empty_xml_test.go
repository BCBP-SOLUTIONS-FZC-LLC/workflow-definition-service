package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestAddModuleVersion_BpmnXmlValue_DecidesWhetherTheServiceRuns(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		body       any
		wantStatus int
		wantCalled bool
		wantXML    *string
	}{
		{name: "empty string is refused", body: map[string]any{"bpmn_xml": ""}, wantStatus: http.StatusBadRequest},
		{name: "text is passed through", body: map[string]any{"bpmn_xml": "<definitions/>"}, wantStatus: http.StatusCreated, wantCalled: true, wantXML: strPtr("<definitions/>")},
		{name: "omitted field copies the active version forward", body: map[string]any{}, wantStatus: http.StatusCreated, wantCalled: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var called bool
			var gotReq service.AddModuleVersionReq
			h := newModuleHandler(&fakeModuleSvc{
				get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.Module, *domain.ModuleVersion, error) {
					return &domain.Module{ID: testWFID, TenantID: &testTenantID, Scope: domain.ScopeTenant}, nil, nil
				},
				addVersion: func(_ context.Context, _, _, _ uuid.UUID, req service.AddModuleVersionReq) (*domain.ModuleVersion, error) {
					called = true
					gotReq = req
					return &domain.ModuleVersion{ID: testVerID, ModuleID: testWFID, Status: domain.VersionStatusDraft}, nil
				},
			})

			w := do(newRouter(h), adminReq(http.MethodPost, "/api/v1/modules/"+testWFID.String()+"/versions", tc.body))

			require.Equal(t, tc.wantStatus, w.Code, w.Body.String())
			assert.Equal(t, tc.wantCalled, called)
			if tc.wantCalled {
				assert.Equal(t, tc.wantXML, gotReq.BPMNXML)
				return
			}
			prob := decodeProblem(t, w)
			assert.Equal(t, "BAD_REQUEST", prob["code"])
			var resp struct {
				InvalidParams []struct {
					Name   string `json:"name"`
					Reason string `json:"reason"`
				} `json:"invalid_params"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			require.Len(t, resp.InvalidParams, 1)
			assert.Equal(t, "bpmn_xml", resp.InvalidParams[0].Name)
		})
	}
}
