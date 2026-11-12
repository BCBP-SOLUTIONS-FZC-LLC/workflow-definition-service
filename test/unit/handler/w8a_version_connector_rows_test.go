package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func w8aVersionPath(suffix string) string {
	return "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + suffix
}

func TestW8aGetVersionValidation_StoredForbiddenConstruct_Is400AndWarnsWithCallerAndTenant(t *testing.T) {
	forbidden := fmt.Errorf("bpmn validate: %w: %w: %v", domain.ErrMalformedBPMN, domain.ErrForbiddenXML, "doctype is not allowed")
	cases := []struct {
		name     string
		err      error
		wantWarn bool
	}{
		{name: "forbidden construct", err: forbidden, wantWarn: true},
		{name: "plain malformed document", err: domain.ErrMalformedBPMN},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			storedXML := `<!DOCTYPE zzq [<!ENTITY zzqent "x">]><definitions/>`
			var gotGet, gotValidate []uuid.UUID
			var validatedXML string
			versions := &fakeVersionSvc{
				get: func(_ context.Context, tenantID, workflowID, versionID uuid.UUID) (*domain.WorkflowVersion, error) {
					gotGet = []uuid.UUID{tenantID, workflowID, versionID}
					v := newPublishedVersion()
					v.BPMNXML = storedXML
					return v, nil
				},
			}
			validation := &fakeValidationSvc{
				validateVersion: func(_ context.Context, tenantID uuid.UUID, v *domain.WorkflowVersion) (bool, []domain.BPMNValidationError, error) {
					gotValidate = []uuid.UUID{tenantID, v.ID}
					validatedXML = v.BPMNXML
					return false, nil, tc.err
				},
			}
			log := &w8aLogger{}
			router := newRouter(handler.New(handler.Services{
				Workflows: &fakeWorkflowSvc{}, Drafts: &fakeDraftSvc{}, Versions: versions, Validation: validation, Log: log,
			}))
			path := w8aVersionPath("/validation")
			r := req(http.MethodGet, path, nil)
			r.RemoteAddr = "198.51.100.23:5555"

			w := do(router, r)

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, "INVALID_BPMN_XML", prob["code"])
			assert.Equal(t, "https://api.workflow.platform/errors/bad-request", prob["type"])
			assert.Equal(t, path, prob["instance"])
			assert.EqualValues(t, http.StatusBadRequest, prob["status"])
			assert.NotContains(t, w.Body.String(), "ENTITY", "the stored document is not echoed")
			assert.Equal(t, []uuid.UUID{testTenantID, testWFID, testVerID}, gotGet)
			assert.Equal(t, []uuid.UUID{testTenantID, testVerID}, gotValidate)
			assert.Equal(t, storedXML, validatedXML)
			warns := log.at("warn")
			if tc.wantWarn {
				require.Len(t, warns, 1)
				assert.Equal(t, "forbidden XML construct rejected", warns[0].msg)
				assert.Equal(t, "198.51.100.23", warns[0].fields["client_ip"])
				assert.Equal(t, testTenantID.String(), warns[0].fields["tenant_id"])
			} else {
				assert.Empty(t, warns)
			}
			assert.Empty(t, log.at("error"), "a 400 is not logged as a server error")
		})
	}
}

func TestW8aGetVersionDiff_PopulatedChanges_AreMappedEntryByEntry(t *testing.T) {
	var got []uuid.UUID
	versions := &fakeVersionSvc{
		diff: func(_ context.Context, tenantID, workflowID, baseID, targetID uuid.UUID) (*service.DiffResult, error) {
			got = []uuid.UUID{tenantID, workflowID, baseID, targetID}
			return &service.DiffResult{
				WorkflowID:      workflowID,
				BaseVersionID:   baseID,
				TargetVersionID: targetID,
				ChangeType:      service.ChangeTypeStructural,
				Changes: service.DiffChanges{
					AddedDepartments:   []string{"dept-added-1", "dept-added-2"},
					RemovedDepartments: []string{"dept-removed"},
					StepChanges: []service.StepChange{
						{Description: "step added", After: &dsl.ExecutionStep{Sequential: []string{"task-new"}}},
						{Description: "step removed", Before: &dsl.ExecutionStep{Sequential: []string{"task-old"}}},
						{
							Description: "step changed",
							Before:      &dsl.ExecutionStep{Sequential: []string{"task-a"}},
							After:       &dsl.ExecutionStep{Sequential: []string{"task-a", "task-b"}},
						},
					},
					StageChanges: []service.StageChange{
						{NodeKey: "stage-added", Description: "stage added"},
						{NodeKey: "stage-removed", Description: "stage removed"},
					},
				},
			}, nil
		},
	}
	router := newRouter(newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, versions, &fakeValidationSvc{}))

	w := do(router, req(http.MethodGet, w8aVersionPath("/diff/"+testVerID2.String()), nil))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, []uuid.UUID{testTenantID, testWFID, testVerID, testVerID2}, got)
	var resp struct {
		WorkflowID      string `json:"workflow_id"`
		BaseVersionID   string `json:"base_version_id"`
		TargetVersionID string `json:"target_version_id"`
		ChangeType      string `json:"change_type"`
		Changes         struct {
			AddedDepartments   []string `json:"added_departments"`
			RemovedDepartments []string `json:"removed_departments"`
			StepChanges        []struct {
				Description string         `json:"description"`
				Before      map[string]any `json:"before"`
				After       map[string]any `json:"after"`
			} `json:"step_changes"`
			StageChanges []struct {
				NodeKey     string `json:"node_key"`
				Description string `json:"description"`
			} `json:"stage_changes"`
			MetadataOnly bool `json:"metadata_only"`
		} `json:"changes"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, testWFID.String(), resp.WorkflowID)
	assert.Equal(t, testVerID.String(), resp.BaseVersionID)
	assert.Equal(t, testVerID2.String(), resp.TargetVersionID)
	assert.Equal(t, "STRUCTURAL", resp.ChangeType)
	assert.Equal(t, []string{"dept-added-1", "dept-added-2"}, resp.Changes.AddedDepartments)
	assert.Equal(t, []string{"dept-removed"}, resp.Changes.RemovedDepartments)
	assert.False(t, resp.Changes.MetadataOnly)

	require.Len(t, resp.Changes.StepChanges, 3)
	assert.Equal(t, "step added", resp.Changes.StepChanges[0].Description)
	assert.Nil(t, resp.Changes.StepChanges[0].Before)
	assert.Equal(t, map[string]any{"sequential": []any{"task-new"}}, resp.Changes.StepChanges[0].After)
	assert.Equal(t, "step removed", resp.Changes.StepChanges[1].Description)
	assert.Equal(t, map[string]any{"sequential": []any{"task-old"}}, resp.Changes.StepChanges[1].Before)
	assert.Nil(t, resp.Changes.StepChanges[1].After)
	assert.Equal(t, "step changed", resp.Changes.StepChanges[2].Description)
	assert.Equal(t, map[string]any{"sequential": []any{"task-a"}}, resp.Changes.StepChanges[2].Before)
	assert.Equal(t, map[string]any{"sequential": []any{"task-a", "task-b"}}, resp.Changes.StepChanges[2].After)

	require.Len(t, resp.Changes.StageChanges, 2)
	assert.Equal(t, "stage-added", resp.Changes.StageChanges[0].NodeKey)
	assert.Equal(t, "stage added", resp.Changes.StageChanges[0].Description)
	assert.Equal(t, "stage-removed", resp.Changes.StageChanges[1].NodeKey)
	assert.Equal(t, "stage removed", resp.Changes.StageChanges[1].Description)
}

func TestW8aGetVersionDiff_NoChanges_ListsAreEmptyArraysNotNull(t *testing.T) {
	cases := []struct {
		name    string
		changes service.DiffChanges
	}{
		{name: "every list nil", changes: service.DiffChanges{MetadataOnly: true}},
		{name: "every list empty but not nil", changes: service.DiffChanges{
			AddedDepartments:   []string{},
			RemovedDepartments: []string{},
			StepChanges:        []service.StepChange{},
			StageChanges:       []service.StageChange{},
			MetadataOnly:       true,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			versions := &fakeVersionSvc{
				diff: func(_ context.Context, _, workflowID, baseID, targetID uuid.UUID) (*service.DiffResult, error) {
					return &service.DiffResult{
						WorkflowID: workflowID, BaseVersionID: baseID, TargetVersionID: targetID,
						ChangeType: service.ChangeTypeMetadataOnly, Changes: tc.changes,
					}, nil
				},
			}
			router := newRouter(newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, versions, &fakeValidationSvc{}))

			w := do(router, req(http.MethodGet, w8aVersionPath("/diff/"+testVerID2.String()), nil))

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			var resp struct {
				ChangeType string                     `json:"change_type"`
				Changes    map[string]json.RawMessage `json:"changes"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.Equal(t, "METADATA_ONLY", resp.ChangeType)
			for _, list := range []string{"added_departments", "removed_departments", "step_changes", "stage_changes"} {
				assert.Equal(t, "[]", string(resp.Changes[list]), "%s is an empty array", list)
			}
			assert.Equal(t, "true", string(resp.Changes["metadata_only"]))
		})
	}
}

type w8aCredentialCall struct {
	tenant, user                uuid.UUID
	connectorType, field, value string
}

type w8aCredentialSvc struct {
	fakeConnectorSvc
	mu    sync.Mutex
	calls []w8aCredentialCall
}

func (s *w8aCredentialSvc) WriteCredential(_ context.Context, tenantID, userID uuid.UUID, connectorType, fieldName, value string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, w8aCredentialCall{tenant: tenantID, user: userID, connectorType: connectorType, field: fieldName, value: value})
	return "connectors/" + tenantID.String() + "/" + connectorType + "/" + fieldName, nil
}

func (s *w8aCredentialSvc) recorded() []w8aCredentialCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]w8aCredentialCall(nil), s.calls...)
}

func TestW8aWriteConnectorCredential_RoleDecidesWhoMayWrite(t *testing.T) {
	cases := []struct {
		name      string
		roles     string
		wantAllow bool
	}{
		{name: "tenant owner", roles: "tenant_owner", wantAllow: true},
		{name: "tenant owner among other roles", roles: "tenant_member,tenant_owner", wantAllow: true},
		{name: "tenant admin", roles: "tenant_admin", wantAllow: true},
		{name: "platform operator only", roles: "platform_operator"},
		{name: "non-admin role", roles: "tenant_member"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := &w8aCredentialSvc{}
			router := newRouter(handler.New(handler.Services{Connectors: svc}))
			path := "/api/v1/connectors/credentials"
			r := req(http.MethodPost, path, map[string]any{"connector_type": "send-email", "field_name": "apiKey", "value": "zzq-secret-value"})
			r.Header.Set("x-tenant-roles", tc.roles)

			w := do(router, r)

			if tc.wantAllow {
				require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
				var resp map[string]string
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
				assert.Equal(t, "connectors/"+testTenantID.String()+"/send-email/apiKey", resp["secret_path"])
				assert.NotContains(t, w.Body.String(), "zzq-secret-value")
				assert.Equal(t, []w8aCredentialCall{{
					tenant: testTenantID, user: testUserID, connectorType: "send-email", field: "apiKey", value: "zzq-secret-value",
				}}, svc.recorded())
				return
			}
			require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, "FORBIDDEN", prob["code"])
			assert.Equal(t, "https://api.workflow.platform/errors/forbidden", prob["type"])
			assert.Equal(t, path, prob["instance"])
			assert.Equal(t, "caller lacks tenant_admin/tenant_owner role", prob["detail"])
			assert.False(t, strings.Contains(w.Body.String(), "zzq-secret-value"))
			assert.Empty(t, svc.recorded(), "the secrets store is not reached")
		})
	}
}
