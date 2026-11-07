package postgres_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
	httpadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func aliasWriteThroughHTTP(t *testing.T, pool *pgcommon.Pool, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	svc := service.NewConnectorService(service.ConnectorDeps{
		Aliases:           postgres.NewConnectorAliasRepo(pool),
		AliasAllowedHosts: []string{".internal"},
	})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	httpadapter.RegisterInternalRoutes(r.Group("/api/v1/internal"), handler.New(handler.Services{Connectors: svc}))

	raw, err := json.Marshal(body)
	require.NoError(t, err)
	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/internal/connector-aliases/rest", bytes.NewReader(raw))
	httpReq.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httpReq)
	return w
}

func TestConnectorAliasWrite_ExistingAlias_ReplacesStoredRow(t *testing.T) {
	cases := []struct {
		name  string
		first map[string]any
		again map[string]any
		want  domain.ConnectorRestAlias
	}{
		{
			name: "every field changes",
			first: map[string]any{
				"alias": "tender-get", "method": "GET",
				"baseURL": "http://tender-service.internal", "pathTemplate": "/old", "timeoutMs": 1000,
			},
			again: map[string]any{
				"alias": "tender-get", "method": "POST",
				"baseURL": "http://crm.internal:8080", "pathTemplate": "/new", "timeoutMs": 2500,
			},
			want: domain.ConnectorRestAlias{
				Alias: "tender-get", Method: "POST",
				BaseURL: "http://crm.internal:8080", PathTemplate: "/new",
				Timeout: 2500 * time.Millisecond,
			},
		},
		{
			name: "timeout omitted on the second write falls back to the default",
			first: map[string]any{
				"alias": "tender-get", "method": "GET",
				"baseURL": "http://tender-service.internal", "pathTemplate": "/x", "timeoutMs": 1000,
			},
			again: map[string]any{
				"alias": "tender-get", "method": "GET",
				"baseURL": "http://tender-service.internal", "pathTemplate": "/x",
			},
			want: domain.ConnectorRestAlias{
				Alias: "tender-get", Method: "GET",
				BaseURL: "http://tender-service.internal", PathTemplate: "/x",
				Timeout: 5 * time.Second,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := newTestPool(t)

			first := aliasWriteThroughHTTP(t, pool, tc.first)
			require.Equal(t, http.StatusNoContent, first.Code, first.Body.String())
			second := aliasWriteThroughHTTP(t, pool, tc.again)
			require.Equal(t, http.StatusNoContent, second.Code, second.Body.String())
			assert.Empty(t, second.Body.String())

			stored, err := postgres.NewConnectorAliasRepo(pool).ListRest(context.Background())
			require.NoError(t, err)
			assert.Equal(t, []domain.ConnectorRestAlias{tc.want}, stored)
		})
	}
}

func TestConnectorAliasWrite_NewAliasName_LeavesOtherAliasUntouched(t *testing.T) {
	pool := newTestPool(t)
	existing := map[string]any{
		"alias": "alias-a", "method": "GET",
		"baseURL": "http://tender-service.internal", "pathTemplate": "/a", "timeoutMs": 1000,
	}
	other := map[string]any{
		"alias": "alias-b", "method": "PUT",
		"baseURL": "http://tender-service.internal", "pathTemplate": "/b", "timeoutMs": 2000,
	}
	require.Equal(t, http.StatusNoContent, aliasWriteThroughHTTP(t, pool, existing).Code)
	require.Equal(t, http.StatusNoContent, aliasWriteThroughHTTP(t, pool, other).Code)

	stored, err := postgres.NewConnectorAliasRepo(pool).ListRest(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []domain.ConnectorRestAlias{
		{Alias: "alias-a", Method: "GET", BaseURL: "http://tender-service.internal", PathTemplate: "/a", Timeout: time.Second},
		{Alias: "alias-b", Method: "PUT", BaseURL: "http://tender-service.internal", PathTemplate: "/b", Timeout: 2 * time.Second},
	}, stored)
}
