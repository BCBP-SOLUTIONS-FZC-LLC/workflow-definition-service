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

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-connectors/pkg/registry"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

type fakeConnectorSvc struct {
	registryFn    func(context.Context) map[string]registry.Definition
	writeFn       func(context.Context, uuid.UUID, string, string, string) (string, error)
	listAliasesFn func(context.Context) ([]domain.ConnectorRestAlias, error)
	writeRestFn   func(context.Context, domain.ConnectorRestAlias) error
	deleteRestFn  func(context.Context, string) (bool, error)
}

func (f *fakeConnectorSvc) Registry(ctx context.Context) map[string]registry.Definition {
	if f.registryFn != nil {
		return f.registryFn(ctx)
	}
	return nil
}

func (f *fakeConnectorSvc) WriteCredential(ctx context.Context, tenantID uuid.UUID, connectorType, fieldName, value string) (string, error) {
	if f.writeFn != nil {
		return f.writeFn(ctx, tenantID, connectorType, fieldName, value)
	}
	return "", nil
}

func (f *fakeConnectorSvc) ListAliases(ctx context.Context) ([]domain.ConnectorRestAlias, error) {
	if f.listAliasesFn != nil {
		return f.listAliasesFn(ctx)
	}
	return nil, nil
}

func (f *fakeConnectorSvc) WriteRestAlias(ctx context.Context, a domain.ConnectorRestAlias) error {
	if f.writeRestFn != nil {
		return f.writeRestFn(ctx, a)
	}
	return nil
}

func (f *fakeConnectorSvc) DeleteRestAlias(ctx context.Context, alias string) (bool, error) {
	if f.deleteRestFn != nil {
		return f.deleteRestFn(ctx, alias)
	}
	return true, nil
}

func newConnectorHandler(conn *fakeConnectorSvc) *handler.Handler {
	return handler.New(handler.Services{Connectors: conn})
}

func TestListConnectorRegistry(t *testing.T) {
	h := newConnectorHandler(&fakeConnectorSvc{
		registryFn: func(context.Context) map[string]registry.Definition {
			return map[string]registry.Definition{
				registry.TypeStorage: {
					Type:        registry.TypeStorage,
					DisplayName: "Storage",
					Inputs:      []registry.Field{{Name: "bucket", Kind: registry.FieldKindString, Required: true}},
					Retry:       registry.RetryPolicySafe,
				},
			}
		},
	})

	w := do(newRouter(h), req(http.MethodGet, "/api/v1/connectors/registry", nil))
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	storage := resp["connectors"][registry.TypeStorage].(map[string]any)
	assert.Equal(t, "Storage", storage["display_name"])
	assert.Equal(t, "safe", storage["retry"])
}

func TestWriteConnectorCredential_Success(t *testing.T) {
	h := newConnectorHandler(&fakeConnectorSvc{
		writeFn: func(_ context.Context, tenantID uuid.UUID, connectorType, fieldName, value string) (string, error) {
			assert.Equal(t, testTenantID, tenantID)
			assert.Equal(t, "send-email", connectorType)
			assert.Equal(t, "apiKey", fieldName)
			assert.Equal(t, "sg-live-abc", value)
			return "connectors/" + tenantID.String() + "/send-email/apiKey", nil
		},
	})

	body := map[string]any{"connector_type": "send-email", "field_name": "apiKey", "value": "sg-live-abc"}
	r := req(http.MethodPost, "/api/v1/connectors/credentials", body)
	r.Header.Set("x-tenant-roles", "tenant_admin")
	w := do(newRouter(h), r)
	assert.Equal(t, http.StatusCreated, w.Code)

	var resp map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Contains(t, resp["secret_path"], "send-email/apiKey")
}

func TestWriteConnectorCredential_Forbidden(t *testing.T) {
	h := newConnectorHandler(&fakeConnectorSvc{})
	w := do(newRouter(h), req(http.MethodPost, "/api/v1/connectors/credentials", map[string]any{"connector_type": "send-email", "field_name": "apiKey", "value": "x"}))
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestWriteConnectorCredential_BindError(t *testing.T) {
	h := newConnectorHandler(&fakeConnectorSvc{})
	r := req(http.MethodPost, "/api/v1/connectors/credentials", map[string]any{"connector_type": "send-email"})
	r.Header.Set("x-tenant-roles", "tenant_admin")
	w := do(newRouter(h), r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestWriteConnectorCredential_UpstreamUnavailable(t *testing.T) {
	h := newConnectorHandler(&fakeConnectorSvc{
		writeFn: func(context.Context, uuid.UUID, string, string, string) (string, error) {
			return "", domain.ErrUpstreamUnavailable
		},
	})

	body := map[string]any{"connector_type": "send-email", "field_name": "apiKey", "value": "x"}
	r := req(http.MethodPost, "/api/v1/connectors/credentials", body)
	r.Header.Set("x-tenant-roles", "tenant_admin")
	w := do(newRouter(h), r)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// TestWriteConnectorCredential_UpstreamUnavailable_NoRawErrorLeak is the
// regression test for the raw-OpenBao-error-leak finding: a wrapped upstream
// error (network error or raw HTTP response body, as secrets_client.go's
// real errors carry) must never reach the client-facing detail field.
func TestWriteConnectorCredential_UpstreamUnavailable_NoRawErrorLeak(t *testing.T) {
	rawUpstreamText := "openbao write \"connectors/tenant/send-email/apiKey\": status 500: {\"errors\":[\"internal storage backend at 10.0.0.5:8200 is sealed\"]}"
	h := newConnectorHandler(&fakeConnectorSvc{
		writeFn: func(context.Context, uuid.UUID, string, string, string) (string, error) {
			return "", fmt.Errorf("write credential: %w: %s", domain.ErrUpstreamUnavailable, rawUpstreamText)
		},
	})

	body := map[string]any{"connector_type": "send-email", "field_name": "apiKey", "value": "x"}
	r := req(http.MethodPost, "/api/v1/connectors/credentials", body)
	r.Header.Set("x-tenant-roles", "tenant_admin")
	w := do(newRouter(h), r)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.NotContains(t, w.Body.String(), "10.0.0.5")
	assert.NotContains(t, w.Body.String(), "sealed")
	assert.NotContains(t, w.Body.String(), rawUpstreamText)
}
