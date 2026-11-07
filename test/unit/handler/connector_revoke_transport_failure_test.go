package handler_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	outbound "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/http"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestRevokeConnectorCredential_SecretsStoreRefusesConnections_Returns503WithGenericDetail(t *testing.T) {
	t.Parallel()

	h := handler.New(handler.Services{Connectors: service.NewConnectorService(service.ConnectorDeps{
		Secrets: outbound.NewSecretsClient("http://127.0.0.1:1", "sgD-token", "secret", 2*time.Second),
	})})

	w := do(newRouter(h), adminReq(http.MethodDelete, revokeCredentialPath, nil))

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	prob := decodeProblem(t, w)
	assert.Equal(t, "UPSTREAM_UNAVAILABLE", prob["code"])
	assert.Equal(t, "an upstream dependency is temporarily unavailable", prob["detail"])
	body := w.Body.String()
	for _, leak := range []string{"127.0.0.1", "connection refused", "openbao", "sgD-token", testTenantID.String()} {
		assert.False(t, strings.Contains(strings.ToLower(body), strings.ToLower(leak)), "response leaks %q: %s", leak, body)
	}
}
