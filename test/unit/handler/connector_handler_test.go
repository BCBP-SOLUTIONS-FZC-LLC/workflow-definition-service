package handler_test

import (
	"context"
	"encoding/json"
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
	registryFn func(context.Context) map[string]registry.Definition
	writeFn    func(context.Context, uuid.UUID, string, string, string) (string, error)
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
	w := do(newRouter(h), req(http.MethodPost, "/api/v1/connectors/credentials", body))
	assert.Equal(t, http.StatusCreated, w.Code)

	var resp map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Contains(t, resp["secret_path"], "send-email/apiKey")
}

func TestWriteConnectorCredential_BindError(t *testing.T) {
	h := newConnectorHandler(&fakeConnectorSvc{})
	w := do(newRouter(h), req(http.MethodPost, "/api/v1/connectors/credentials", map[string]any{"connector_type": "send-email"}))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestWriteConnectorCredential_UpstreamUnavailable(t *testing.T) {
	h := newConnectorHandler(&fakeConnectorSvc{
		writeFn: func(context.Context, uuid.UUID, string, string, string) (string, error) {
			return "", domain.ErrUpstreamUnavailable
		},
	})

	body := map[string]any{"connector_type": "send-email", "field_name": "apiKey", "value": "x"}
	w := do(newRouter(h), req(http.MethodPost, "/api/v1/connectors/credentials", body))
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}
