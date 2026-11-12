package handler_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type w8dStarterCreateRecorder struct {
	calls  int
	tenant uuid.UUID
	user   uuid.UUID
	req    service.CreateStarterReq
}

func (r *w8dStarterCreateRecorder) fake() *fakeStarterSvc {
	return &fakeStarterSvc{
		create: func(_ context.Context, tenantID, userID uuid.UUID, req service.CreateStarterReq) (*domain.StarterTemplate, error) {
			r.calls++
			r.tenant, r.user, r.req = tenantID, userID, req
			return &domain.StarterTemplate{
				ID: testWFID, TenantID: &tenantID, Scope: req.Scope, Name: req.Name,
				Description: req.Description, Category: req.Category, BPMNXML: req.BPMNXML,
			}, nil
		},
	}
}

type w8dStarterFromVersionRecorder struct {
	calls   int
	tenant  uuid.UUID
	user    uuid.UUID
	version uuid.UUID
	req     service.CreateFromWorkflowVersionReq
}

func (r *w8dStarterFromVersionRecorder) fake() *fakeStarterSvc {
	return &fakeStarterSvc{
		createFromWorkflowVersion: func(_ context.Context, tenantID, userID, versionID uuid.UUID, req service.CreateFromWorkflowVersionReq) (*domain.StarterTemplate, error) {
			r.calls++
			r.tenant, r.user, r.version, r.req = tenantID, userID, versionID, req
			return &domain.StarterTemplate{
				ID: testWFID, TenantID: &tenantID, Scope: domain.ScopeTenant, Name: req.Name,
				Description: req.Description, Category: req.Category, SourceWorkflowVersionID: &versionID,
			}, nil
		},
	}
}

func TestCreateStarter_ExplicitTenantScope_Returns201WithScopeTenant(t *testing.T) {
	t.Parallel()
	rec := &w8dStarterCreateRecorder{}
	h := newStarterHandler(rec.fake())

	w := do(newRouter(h), adminReq(http.MethodPost, "/api/v1/starters",
		map[string]any{"name": "Review", "bpmn_xml": "<bpmn/>", "scope": "tenant"}))

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	require.Equal(t, 1, rec.calls)
	assert.Equal(t, domain.ScopeTenant, rec.req.Scope)
	assert.Equal(t, testTenantID, rec.tenant)
	assert.Equal(t, testUserID, rec.user)
	body := w8dBody(t, w)
	assert.Equal(t, "tenant", body["scope"])
	assert.Equal(t, testTenantID.String(), body["tenant_id"])
	assert.Equal(t, "Review", body["name"])
}

func TestCreateStarter_DescriptionAndCategoryOmitted_Returns201WithEmptyDescriptionAndNoCategory(t *testing.T) {
	t.Parallel()
	rec := &w8dStarterCreateRecorder{}
	h := newStarterHandler(rec.fake())

	w := do(newRouter(h), adminReq(http.MethodPost, "/api/v1/starters",
		map[string]any{"name": "Review", "bpmn_xml": "<bpmn/>"}))

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	require.Equal(t, 1, rec.calls)
	assert.Empty(t, rec.req.Description)
	assert.Empty(t, rec.req.Category)
	body := w8dBody(t, w)
	description, hasDescription := body["description"]
	assert.True(t, hasDescription, "the description key is always present")
	assert.Equal(t, "", description)
	assert.NotContains(t, body, "category", "an empty category is left out of the body")
}

func TestCreateStarter_NameOrBpmnXmlMissingOrEmpty_Returns400NamingEachField(t *testing.T) {
	t.Parallel()
	type param struct{ name, reason string }
	cases := []struct {
		name string
		body map[string]any
		want []param
	}{
		{
			name: "name omitted",
			body: map[string]any{"bpmn_xml": "<bpmn/>"},
			want: []param{{"name", "is required"}},
		},
		{
			name: "name empty",
			body: map[string]any{"name": "", "bpmn_xml": "<bpmn/>"},
			want: []param{{"name", "is required"}},
		},
		{
			name: "bpmn_xml omitted",
			body: map[string]any{"name": "Review"},
			want: []param{{"bpmn_xml", "is required"}},
		},
		{
			name: "bpmn_xml empty",
			body: map[string]any{"name": "Review", "bpmn_xml": ""},
			want: []param{{"bpmn_xml", "is required"}},
		},
		{
			name: "empty object",
			body: map[string]any{},
			want: []param{{"name", "is required"}, {"bpmn_xml", "is required"}},
		},
		{
			name: "both empty",
			body: map[string]any{"name": "", "bpmn_xml": ""},
			want: []param{{"name", "is required"}, {"bpmn_xml", "is required"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := &w8dStarterCreateRecorder{}
			h := newStarterHandler(rec.fake())

			w := do(newRouter(h), adminReq(http.MethodPost, "/api/v1/starters", tc.body))

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			p := w8dDecodeProblem(t, w)
			assert.Equal(t, w8dTypeBadRequest, p.Type)
			assert.Equal(t, http.StatusBadRequest, p.Status)
			assert.Equal(t, "BAD_REQUEST", p.Code)
			assert.Equal(t, w8dDetailBadBody, p.Detail)
			assert.Equal(t, "/api/v1/starters", p.Instance)
			got := make([]param, len(p.InvalidParams))
			for i, ip := range p.InvalidParams {
				got[i] = param{ip.Name, ip.Reason}
			}
			assert.Equal(t, tc.want, got, "entries come in field order")
			assert.Zero(t, rec.calls, "a refused body never reaches the service")
		})
	}
}

func TestCreateStarterFromWorkflowVersion_OnlyNameSent_Returns201WithEmptyDescriptionAndNoCategory(t *testing.T) {
	t.Parallel()
	rec := &w8dStarterFromVersionRecorder{}
	h := newStarterHandler(rec.fake())

	w := do(newRouter(h), adminReq(http.MethodPost,
		"/api/v1/starters/from-workflow-version/"+testVerID.String(), map[string]any{"name": "Onboarding"}))

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	require.Equal(t, 1, rec.calls)
	assert.Equal(t, testTenantID, rec.tenant)
	assert.Equal(t, testUserID, rec.user)
	assert.Equal(t, testVerID, rec.version)
	assert.Equal(t, "Onboarding", rec.req.Name)
	assert.Empty(t, rec.req.Description)
	assert.Empty(t, rec.req.Category)
	body := w8dBody(t, w)
	description, hasDescription := body["description"]
	assert.True(t, hasDescription, "the description key is always present")
	assert.Equal(t, "", description)
	assert.NotContains(t, body, "category", "an empty category is left out of the body")
	assert.Equal(t, testVerID.String(), body["source_workflow_version_id"])
}
