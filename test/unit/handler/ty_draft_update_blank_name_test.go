package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestUpdateDraft_BlankName_Returns422LikeCreateAndNeverReachesTheService(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"", "   ", " \n\t"} {
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			t.Parallel()
			calls := 0
			h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{
				update: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, service.UpdateDraftReq) (*domain.WorkflowVersion, error) {
					calls++
					return newDraftVersion(), nil
				},
			}, &fakeVersionSvc{}, &fakeValidationSvc{})

			w := do(newRouter(h), adminReq(http.MethodPut, "/api/v1/workflows/"+testWFID.String()+"/draft",
				map[string]any{"name": name, "record_version": 1}))

			require.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
			var p struct {
				Code          string `json:"code"`
				Detail        string `json:"detail"`
				InvalidParams []struct {
					Name   string `json:"name"`
					Reason string `json:"reason"`
				} `json:"invalid_params"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &p))
			assert.Equal(t, "INVALID_INPUT", p.Code, "the same code as the blank-name rule on create")
			assert.Equal(t, "request fields must not be blank", p.Detail)
			require.Len(t, p.InvalidParams, 1)
			assert.Equal(t, "name", p.InvalidParams[0].Name)
			assert.Equal(t, "must not be blank", p.InvalidParams[0].Reason)
			assert.Zero(t, calls)
		})
	}
}

func TestUpdateDraft_BlankDescription_IsStillAllowed(t *testing.T) {
	t.Parallel()
	var got service.UpdateDraftReq
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{
		update: func(_ context.Context, _, _, _ uuid.UUID, r service.UpdateDraftReq) (*domain.WorkflowVersion, error) {
			got = r
			return newDraftVersion(), nil
		},
	}, &fakeVersionSvc{}, &fakeValidationSvc{})

	w := do(newRouter(h), adminReq(http.MethodPut, "/api/v1/workflows/"+testWFID.String()+"/draft",
		map[string]any{"description": "  ", "record_version": 1}))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, tyStrPtr("  "), got.Description)
}

func TestUpdateDraft_OmittedOrFilledName_Returns200AndForwardsTheNameExactlyAsSent(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		body     map[string]any
		wantName *string
	}{
		{"filled name keeps its spaces", map[string]any{"name": "  Renamed ", "record_version": 1}, tyStrPtr("  Renamed ")},
		{"omitted name", map[string]any{"description": "only the description", "record_version": 1}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var gotTenant, gotWorkflow uuid.UUID
			var got service.UpdateDraftReq
			calls := 0
			h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{
				update: func(_ context.Context, tenantID, _, workflowID uuid.UUID, r service.UpdateDraftReq) (*domain.WorkflowVersion, error) {
					calls++
					gotTenant, gotWorkflow, got = tenantID, workflowID, r
					v := newDraftVersion()
					v.RecordVersion = 2
					return v, nil
				},
			}, &fakeVersionSvc{}, &fakeValidationSvc{})

			w := do(newRouter(h), adminReq(http.MethodPut, "/api/v1/workflows/"+testWFID.String()+"/draft", tc.body))

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.Equal(t, "Draft updated", resp["message"])
			assert.EqualValues(t, 2, resp["record_version"])
			require.Equal(t, 1, calls)
			assert.Equal(t, testTenantID, gotTenant)
			assert.Equal(t, testWFID, gotWorkflow)
			assert.Equal(t, tc.wantName, got.Name)
		})
	}
}

func tyStrPtr(s string) *string { return &s }
