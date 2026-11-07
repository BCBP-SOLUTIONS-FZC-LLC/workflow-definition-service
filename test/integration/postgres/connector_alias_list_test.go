package postgres_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
	httpadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

type aliasListBody struct {
	Version  int                  `json:"version"`
	RestCall []aliasListBodyEntry `json:"restCall"`
}

type aliasListBodyEntry struct {
	Alias        string `json:"alias"`
	Method       string `json:"method"`
	BaseURL      string `json:"baseURL"`
	PathTemplate string `json:"pathTemplate"`
	Timeout      int64  `json:"timeout"`
}

func listAliasesThroughHTTP(t *testing.T, pool *pgcommon.Pool) (*httptest.ResponseRecorder, string) {
	t.Helper()
	svc := service.NewConnectorService(service.ConnectorDeps{
		Aliases: postgres.NewConnectorAliasRepo(pool),
	})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	httpadapter.RegisterInternalRoutes(r.Group("/api/v1/internal"), handler.New(handler.Services{Connectors: svc}))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/internal/connector-aliases", nil))
	return w, w.Body.String()
}

func TestConnectorAliasList_NoAliasesStored_ReturnsEmptyArray(t *testing.T) {
	pool := newTestPool(t)

	got, err := postgres.NewConnectorAliasRepo(pool).ListRest(context.Background())
	require.NoError(t, err)
	assert.NotNil(t, got)
	assert.Empty(t, got)

	w, raw := listAliasesThroughHTTP(t, pool)
	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(raw), &body))
	assert.JSONEq(t, `1`, string(body["version"]))
	assert.JSONEq(t, `[]`, string(body["restCall"]))
}

func TestConnectorAliasList_StoredTimeout_ReturnedInNanoseconds(t *testing.T) {
	cases := []struct {
		name        string
		timeout     time.Duration
		wantTimeout int64
	}{
		{name: "five thousand milliseconds", timeout: 5000 * time.Millisecond, wantTimeout: 5_000_000_000},
		{name: "zero milliseconds", timeout: 0, wantTimeout: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := newTestPool(t)
			repo := postgres.NewConnectorAliasRepo(pool)
			require.NoError(t, repo.UpsertRest(context.Background(), domain.ConnectorRestAlias{
				Alias: "tender-get", Method: "GET",
				BaseURL: "http://tender-service.internal", PathTemplate: "/x",
				Timeout: tc.timeout,
			}))

			w, raw := listAliasesThroughHTTP(t, pool)
			require.Equal(t, http.StatusOK, w.Code)
			var body aliasListBody
			require.NoError(t, json.Unmarshal([]byte(raw), &body))
			assert.Equal(t, 1, body.Version)
			require.Len(t, body.RestCall, 1)
			assert.Equal(t, aliasListBodyEntry{
				Alias: "tender-get", Method: "GET",
				BaseURL: "http://tender-service.internal", PathTemplate: "/x",
				Timeout: tc.wantTimeout,
			}, body.RestCall[0])
		})
	}
}

func TestConnectorAliasList_AnyCallerOrTenant_SeesEveryAlias(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	ctx := context.Background()
	seed := postgres.NewConnectorAliasRepo(superPool)
	for _, alias := range []string{"alias-b", "alias-a", "alias-c"} {
		require.NoError(t, seed.UpsertRest(ctx, domain.ConnectorRestAlias{
			Alias: alias, Method: "GET",
			BaseURL: "http://tender-service.internal", PathTemplate: "/" + alias,
			Timeout: 5 * time.Second,
		}))
	}

	tenantA, userA := uuid.New(), uuid.New()
	tenantB, userB := uuid.New(), uuid.New()
	callers := map[string]*pgcommon.Pool{
		"tenant A":  fixtures.NewRestrictedRolePool(t, dsn, false, &tenantA, &userA),
		"tenant B":  fixtures.NewRestrictedRolePool(t, dsn, false, &tenantB, &userB),
		"no tenant": fixtures.NewRestrictedRolePool(t, dsn, false, nil, nil),
	}
	for name, pool := range callers {
		t.Run(name, func(t *testing.T) {
			w, raw := listAliasesThroughHTTP(t, pool)
			require.Equal(t, http.StatusOK, w.Code)
			var body aliasListBody
			require.NoError(t, json.Unmarshal([]byte(raw), &body))
			names := make([]string, len(body.RestCall))
			for i, a := range body.RestCall {
				names[i] = a.Alias
			}
			assert.Equal(t, []string{"alias-a", "alias-b", "alias-c"}, names)
		})
	}
}
