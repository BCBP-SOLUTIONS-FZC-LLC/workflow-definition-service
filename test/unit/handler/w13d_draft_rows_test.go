package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	bpmncompiler "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

const w13dProblemBase = "https://api.workflow.platform/errors/"

type w13dLogEntry struct {
	msg    string
	fields map[string]any
}

type w13dInfoLogger struct {
	mu    sync.Mutex
	infos []w13dLogEntry
}

func (l *w13dInfoLogger) Debug(string, map[string]any) { /* no-op */ }
func (l *w13dInfoLogger) Warn(string, map[string]any)  { /* no-op */ }
func (l *w13dInfoLogger) Error(string, map[string]any) { /* no-op */ }
func (l *w13dInfoLogger) Fatal(string, map[string]any) { /* no-op */ }
func (l *w13dInfoLogger) Info(msg string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.infos = append(l.infos, w13dLogEntry{msg: msg, fields: fields})
}

func (l *w13dInfoLogger) recorded() []w13dLogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]w13dLogEntry(nil), l.infos...)
}

func w13dDraftPath() string {
	return "/api/v1/workflows/" + testWFID.String() + "/draft"
}

func w13dRawAdminReq(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-tenant-id", testTenantID.String())
	r.Header.Set("x-user-id", testUserID.String())
	r.Header.Set("x-tenant-roles", "tenant_admin")
	return r
}

func TestUpdateDraft_NameAndDescriptionOmitted_Returns200AndPassesNilToTheService(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name            string
		body            string
		wantDescription *string
	}{
		{name: "both members absent", body: `{"bpmn_xml":"<definitions/>","record_version":3}`},
		{name: "both members null", body: `{"name":null,"description":null,"bpmn_xml":"<definitions/>","record_version":3}`},
		{
			name:            "name absent while description is sent",
			body:            `{"description":"only the description","record_version":3}`,
			wantDescription: new(string),
		},
	}
	*cases[2].wantDescription = "only the description"

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var got *service.UpdateDraftReq
			h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{
				update: func(_ context.Context, tenant, user, wf uuid.UUID, r service.UpdateDraftReq) (*domain.WorkflowVersion, error) {
					assert.Equal(t, testTenantID, tenant)
					assert.Equal(t, testUserID, user)
					assert.Equal(t, testWFID, wf)
					got = &r
					return newDraftVersion(), nil
				},
			}, &fakeVersionSvc{}, &fakeValidationSvc{})

			w := do(newRouter(h), w13dRawAdminReq(http.MethodPut, w13dDraftPath(), tc.body))

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			require.NotNil(t, got, "the request reaches the draft service")
			assert.Nil(t, got.Name, "an omitted name stays unset, it is not turned into an empty string")
			if tc.wantDescription == nil {
				assert.Nil(t, got.Description)
			} else {
				require.NotNil(t, got.Description)
				assert.Equal(t, *tc.wantDescription, *got.Description)
			}
			assert.EqualValues(t, 3, got.RecordVersion)
		})
	}
}

func TestUpdateDraft_EmptyOrWhitespaceBody_Returns400NotValidJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{name: "empty body", body: ""},
		{name: "spaces only", body: "     "},
		{name: "newlines and tabs only", body: "\n\t\r\n\t"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var reached bool
			log := &fakeLogger{}
			h := handler.New(handler.Services{
				Drafts: &fakeDraftSvc{
					update: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, service.UpdateDraftReq) (*domain.WorkflowVersion, error) {
						reached = true
						return newDraftVersion(), nil
					},
				},
				Log: log,
			})

			w := do(newRouter(h), w13dRawAdminReq(http.MethodPut, w13dDraftPath(), tc.body))

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			assert.False(t, reached, "an empty body never reaches the draft service")
			prob := decodeProblem(t, w)
			assert.Equal(t, w13dProblemBase+"bad-request", prob["type"])
			assert.Equal(t, "BAD_REQUEST", prob["code"])
			assert.Equal(t, "request body is not valid JSON", prob["detail"])
			assert.EqualValues(t, http.StatusBadRequest, prob["status"])
			assert.Equal(t, w13dDraftPath(), prob["instance"])
			assert.NotContains(t, prob, "invalid_params")
			assert.Len(t, log.warnCalls, 1, "the raw bind error is logged, not returned")
		})
	}
}

func TestUpdateDraft_RecordVersionOfTheWrongType_Returns400NamingTheField(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{name: "a numeric string", body: `{"name":"x","record_version":"3"}`},
		{name: "a boolean", body: `{"name":"x","record_version":true}`},
		{name: "an object", body: `{"name":"x","record_version":{"n":3}}`},
		{name: "a fraction", body: `{"name":"x","record_version":3.5}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var reached bool
			log := &fakeLogger{}
			h := handler.New(handler.Services{
				Drafts: &fakeDraftSvc{
					update: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, service.UpdateDraftReq) (*domain.WorkflowVersion, error) {
						reached = true
						return newDraftVersion(), nil
					},
				},
				Log: log,
			})

			w := do(newRouter(h), w13dRawAdminReq(http.MethodPut, w13dDraftPath(), tc.body))

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			assert.False(t, reached, "a refused body never reaches the draft service")
			var prob struct {
				Type          string `json:"type"`
				Code          string `json:"code"`
				Detail        string `json:"detail"`
				Status        int    `json:"status"`
				Instance      string `json:"instance"`
				InvalidParams []struct {
					Name   string `json:"name"`
					Reason string `json:"reason"`
				} `json:"invalid_params"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &prob))
			assert.Equal(t, w13dProblemBase+"bad-request", prob.Type)
			assert.Equal(t, "BAD_REQUEST", prob.Code)
			assert.Equal(t, "request body has a field of the wrong type", prob.Detail)
			assert.Equal(t, http.StatusBadRequest, prob.Status)
			assert.Equal(t, w13dDraftPath(), prob.Instance)
			require.Len(t, prob.InvalidParams, 1)
			assert.Equal(t, "record_version", prob.InvalidParams[0].Name)
			assert.Equal(t, "has the wrong type", prob.InvalidParams[0].Reason)
			assert.Len(t, log.warnCalls, 1, "the raw bind error is logged, not returned")
		})
	}
}

func TestUpdateDraft_RealService_BPMNOnly_AnswersTheStoredDraftAndIncrementedRecordVersion(t *testing.T) {
	t.Parallel()

	const newXML = "<definitions/>"
	ctrl := gomock.NewController(t)
	versions := mocks.NewMockWorkflowVersionRepository(ctrl)
	workflows := mocks.NewMockWorkflowRepository(ctrl)
	stored := newDraftVersion()
	stored.BPMNXML = "<definitions><old/></definitions>"
	stored.RecordVersion = 3
	versions.EXPECT().GetDraft(gomock.Any(), testTenantID, testWFID).Return(stored, nil)
	var saved *domain.WorkflowVersion
	versions.EXPECT().UpdateDraft(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, v *domain.WorkflowVersion) error {
			saved = v
			v.RecordVersion++
			return nil
		})
	svc := service.NewDraftService(service.DraftDeps{
		Workflows: workflows,
		Versions:  versions,
		Compiler:  bpmncompiler.New(),
	})
	router := newRouter(handler.New(handler.Services{Drafts: svc}))

	w := do(router, adminReq(http.MethodPut, w13dDraftPath(), map[string]any{"bpmn_xml": newXML, "record_version": 3}))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NotNil(t, saved, "the draft is written to the repository")
	assert.Equal(t, newXML, saved.BPMNXML)
	assert.Equal(t, stored.ID, saved.ID)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, testWFID.String(), resp["workflow_id"])
	assert.Equal(t, stored.ID.String(), resp["version_id"])
	assert.Equal(t, "DRAFT", resp["status"])
	assert.EqualValues(t, 4, resp["record_version"], "the answer carries the version the repository returned, not the one sent")
	assert.Equal(t, "Draft updated", resp["message"])
	assert.Contains(t, resp, "version_number")
}

func TestDiscardDraft_CallerWithoutAnAdminRole_Returns403Forbidden(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		roles string
	}{
		{name: "a member role", roles: "tenant_member"},
		{name: "the platform operator role alone", roles: "platform_operator"},
		{name: "a role that only resembles an admin role", roles: "tenant_administrator"},
		{name: "several roles none of them admin", roles: "tenant_member,viewer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var discarded bool
			h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{
				discard: func(context.Context, uuid.UUID, uuid.UUID) error {
					discarded = true
					return nil
				},
			}, &fakeVersionSvc{}, &fakeValidationSvc{})
			r := req(http.MethodDelete, w13dDraftPath(), nil)
			r.Header.Set("x-tenant-roles", tc.roles)

			w := do(newRouter(h), r)

			require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
			assert.False(t, discarded, "a caller without an admin role never reaches the draft service")
			prob := decodeProblem(t, w)
			assert.Equal(t, w13dProblemBase+"forbidden", prob["type"])
			assert.Equal(t, "FORBIDDEN", prob["code"])
			assert.Equal(t, "caller lacks tenant_admin/tenant_owner role", prob["detail"])
			assert.EqualValues(t, http.StatusForbidden, prob["status"])
			assert.Equal(t, w13dDraftPath(), prob["instance"])
		})
	}
}

func TestDiscardDraft_RealService_ExistingDraft_Returns204WithEmptyBodyAndLogsIt(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	versions := mocks.NewMockWorkflowVersionRepository(ctrl)
	workflows := mocks.NewMockWorkflowRepository(ctrl)
	draft := newDraftVersion()
	deleted := false
	versions.EXPECT().GetDraft(gomock.Any(), testTenantID, testWFID).DoAndReturn(
		func(context.Context, uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
			if deleted {
				return nil, domain.ErrNotFound
			}
			return draft, nil
		}).Times(2)
	versions.EXPECT().DeleteDraft(gomock.Any(), testTenantID, draft.ID).DoAndReturn(
		func(context.Context, uuid.UUID, uuid.UUID) error {
			deleted = true
			return nil
		})
	workflows.EXPECT().GetByID(gomock.Any(), testTenantID, testWFID).Return(newWorkflow(), nil)
	log := &w13dInfoLogger{}
	svc := service.NewDraftService(service.DraftDeps{Workflows: workflows, Versions: versions, Log: log})
	router := newRouter(handler.New(handler.Services{Drafts: svc, Log: log}))

	w := do(router, adminReq(http.MethodDelete, w13dDraftPath(), nil))

	require.Equal(t, http.StatusNoContent, w.Code)
	assert.Empty(t, w.Body.String())
	assert.Empty(t, w.Header().Get("Content-Type"), "a 204 carries no content type")
	entries := log.recorded()
	require.Len(t, entries, 1)
	assert.Equal(t, "draft discarded", entries[0].msg)
	assert.Equal(t, testTenantID.String(), entries[0].fields["tenant_id"])
	assert.Equal(t, testWFID.String(), entries[0].fields["workflow_id"])
	assert.Equal(t, draft.ID.String(), entries[0].fields["version_id"])

	after := do(router, adminReq(http.MethodGet, w13dDraftPath(), nil))
	require.Equal(t, http.StatusNotFound, after.Code, after.Body.String())
	assert.Equal(t, "DRAFT_NOT_FOUND", decodeProblem(t, after)["code"], "the discarded draft can no longer be read")
}
