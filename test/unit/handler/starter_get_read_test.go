package handler_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func starterGetReadFake(st *domain.StarterTemplate, err error) *fakeStarterSvc {
	return &fakeStarterSvc{
		get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.StarterTemplate, error) {
			return st, err
		},
	}
}

func TestGetStarter_OptionalFields_AreOmittedWhenUnset(t *testing.T) {
	t.Parallel()
	sourceVersion := uuid.New()
	tenant := uuid.New()

	cases := []struct {
		name        string
		starter     *domain.StarterTemplate
		wantPresent map[string]any
		wantAbsent  []string
	}{
		{
			name: "global starter with no category and no source version",
			starter: &domain.StarterTemplate{
				ID: testWFID, Scope: domain.ScopeGlobal, Name: "Onboarding", BPMNXML: "<bpmn/>",
			},
			wantPresent: map[string]any{
				"id": testWFID.String(), "scope": "global", "name": "Onboarding", "description": "", "bpmn_xml": "<bpmn/>",
			},
			wantAbsent: []string{"category", "source_workflow_version_id", "tenant_id"},
		},
		{
			name: "tenant starter with category and source version",
			starter: &domain.StarterTemplate{
				ID: testWFID, TenantID: &tenant, Scope: domain.ScopeTenant, Name: "Onboarding",
				Category: "hr", BPMNXML: "<bpmn/>", SourceWorkflowVersionID: &sourceVersion,
			},
			wantPresent: map[string]any{
				"category": "hr", "source_workflow_version_id": sourceVersion.String(),
				"tenant_id": tenant.String(), "scope": "tenant",
			},
			wantAbsent: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newStarterHandler(starterGetReadFake(tc.starter, nil))

			w := do(newRouter(h), req(http.MethodGet, "/api/v1/starters/"+testWFID.String(), nil))

			require.Equal(t, http.StatusOK, w.Code)
			body := decodeProblem(t, w)
			for k, v := range tc.wantPresent {
				assert.Equal(t, v, body[k], "field %s", k)
			}
			for _, k := range tc.wantAbsent {
				assert.NotContains(t, body, k)
			}
		})
	}
}

func TestGetStarter_ServiceError_MapsToProblem(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		wantType   string
	}{
		{
			name:       "database unreachable",
			err:        fmt.Errorf("get starter: %w: %w", domain.ErrUpstreamUnavailable, errors.New("closed pool")),
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "UPSTREAM_UNAVAILABLE",
			wantType:   "service-unavailable",
		},
		{
			name:       "non-connection database failure",
			err:        fmt.Errorf("get starter: %w", errors.New("syntax error at or near")),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL_ERROR",
			wantType:   "internal-error",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newStarterHandler(starterGetReadFake(nil, tc.err))

			w := do(newRouter(h), req(http.MethodGet, "/api/v1/starters/"+testWFID.String(), nil))

			require.Equal(t, tc.wantStatus, w.Code)
			body := decodeProblem(t, w)
			assert.Equal(t, tc.wantCode, body["code"])
			assert.True(t, strings.HasSuffix(fmt.Sprint(body["type"]), "/errors/"+tc.wantType), "type=%v", body["type"])
			assert.NotContains(t, fmt.Sprint(body["detail"]), "closed pool")
			assert.NotContains(t, fmt.Sprint(body["detail"]), "syntax error")
		})
	}
}

func TestGetStarter_AlternateUUIDSpellings_ResolveToTheSameStarter(t *testing.T) {
	t.Parallel()
	id := uuid.MustParse("abcdef01-2345-6789-abcd-ef0123456789")
	cases := []struct {
		name string
		path string
	}{
		{name: "uppercase", path: strings.ToUpper(id.String())},
		{name: "32 hex digits without hyphens", path: strings.ReplaceAll(id.String(), "-", "")},
		{name: "urn prefix", path: "urn:uuid:" + id.String()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var gotID, gotTenant uuid.UUID
			h := newStarterHandler(&fakeStarterSvc{
				get: func(_ context.Context, tenantID, starterID uuid.UUID) (*domain.StarterTemplate, error) {
					gotID, gotTenant = starterID, tenantID
					return &domain.StarterTemplate{ID: starterID, Scope: domain.ScopeGlobal, Name: "s"}, nil
				},
			})

			w := do(newRouter(h), req(http.MethodGet, "/api/v1/starters/"+tc.path, nil))

			require.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, id, gotID)
			assert.Equal(t, testTenantID, gotTenant)
			assert.Equal(t, id.String(), decodeProblem(t, w)["id"])
		})
	}
}
