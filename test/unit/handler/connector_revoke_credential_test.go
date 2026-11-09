package handler_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	outbound "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/http"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

const revokeCredentialPath = "/api/v1/connectors/credentials/send-email/apiKey"

func TestRevokeConnectorCredential_MissingIdentityContext_Returns500(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
	}{
		{name: "route mounted without the identity middleware"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			called := false
			h := newConnectorHandler(&fakeConnectorSvc{
				revokeFn: func(context.Context, uuid.UUID, string, string) error {
					called = true
					return nil
				},
			})

			r := adminReq(http.MethodDelete, revokeCredentialPath, nil)
			w := do(newBareRouter(h), r)

			require.Equal(t, http.StatusInternalServerError, w.Code)
			assert.Equal(t, "INTERNAL_ERROR", decodeProblem(t, w)["code"])
			assert.False(t, called, "the credential must not be revoked without an identity")
		})
	}
}

func TestRevokeConnectorCredential_AdminRole_Returns204(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		roles string
	}{
		{name: "tenant owner only", roles: "tenant_owner"},
		{name: "tenant admin only", roles: "tenant_admin"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var gotTenant uuid.UUID
			var gotType, gotField string
			revoked := false
			h := newConnectorHandler(&fakeConnectorSvc{
				revokeFn: func(_ context.Context, tenantID uuid.UUID, connectorType, fieldName string) error {
					revoked = true
					gotTenant, gotType, gotField = tenantID, connectorType, fieldName
					return nil
				},
			})

			r := req(http.MethodDelete, revokeCredentialPath, nil)
			r.Header.Set("x-tenant-roles", tc.roles)
			w := do(newRouter(h), r)

			require.Equal(t, http.StatusNoContent, w.Code)
			assert.Empty(t, w.Body.String())
			assert.True(t, revoked)
			assert.Equal(t, testTenantID, gotTenant)
			assert.Equal(t, "send-email", gotType)
			assert.Equal(t, "apiKey", gotField)
		})
	}
}

func TestRevokeConnectorCredential_SecretsStoreFailure_MapsToProblem(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		deps       func() service.ConnectorDeps
		wantStatus int
		wantCode   string
	}{
		{
			name:       "secrets store not configured",
			deps:       func() service.ConnectorDeps { return service.ConnectorDeps{} },
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "UPSTREAM_UNAVAILABLE",
		},
		{
			name: "secrets store address is malformed",
			deps: func() service.ConnectorDeps {
				return service.ConnectorDeps{
					Secrets: outbound.NewSecretsClient("http://[::1", "token", "secret", time.Second),
				}
			},
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL_ERROR",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := handler.New(handler.Services{Connectors: service.NewConnectorService(tc.deps())})

			w := do(newRouter(h), adminReq(http.MethodDelete, revokeCredentialPath, nil))

			require.Equal(t, tc.wantStatus, w.Code)
			assert.Equal(t, tc.wantCode, decodeProblem(t, w)["code"])
		})
	}
}
