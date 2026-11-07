//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
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

func sgCAliasServe(t *testing.T, pool *pgcommon.Pool, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	svc := service.NewConnectorService(service.ConnectorDeps{
		Aliases: postgres.NewConnectorAliasRepo(pool),
	})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	httpadapter.RegisterInternalRoutes(r.Group("/api/v1/internal"), handler.New(handler.Services{Connectors: svc}))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	return w
}

func sgCAliasSeed(t *testing.T, pool *pgcommon.Pool, aliases ...domain.ConnectorRestAlias) {
	t.Helper()
	repo := postgres.NewConnectorAliasRepo(pool)
	for _, a := range aliases {
		require.NoError(t, repo.UpsertRest(context.Background(), a))
	}
}

func TestConnectorAliasList_SeveralAliasesStoredOutOfOrder_ReturnsThemSortedWithEveryField(t *testing.T) {
	pool := newTestPool(t)
	sgCAliasSeed(t, pool,
		domain.ConnectorRestAlias{Alias: "tender-update", Method: "PUT", BaseURL: "http://tender-service.internal", PathTemplate: "/tenders/{id}", Timeout: 7 * time.Second},
		domain.ConnectorRestAlias{Alias: "crm-lookup", Method: "GET", BaseURL: "http://crm.internal:8080", PathTemplate: "/customers/{id}", Timeout: 1500 * time.Millisecond},
		domain.ConnectorRestAlias{Alias: "notify-post", Method: "POST", BaseURL: "https://notify.internal", PathTemplate: "/v1/send", Timeout: 30 * time.Second},
	)

	w := sgCAliasServe(t, pool, http.MethodGet, "/api/v1/internal/connector-aliases")

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var body struct {
		Version  int `json:"version"`
		RestCall []struct {
			Alias        string `json:"alias"`
			Method       string `json:"method"`
			BaseURL      string `json:"baseURL"`
			PathTemplate string `json:"pathTemplate"`
			Timeout      int64  `json:"timeout"`
		} `json:"restCall"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, 1, body.Version)

	type entry struct {
		Alias, Method, BaseURL, PathTemplate string
		Timeout                              int64
	}
	got := make([]entry, len(body.RestCall))
	for i, e := range body.RestCall {
		got[i] = entry{e.Alias, e.Method, e.BaseURL, e.PathTemplate, e.Timeout}
	}
	assert.Equal(t, []entry{
		{"crm-lookup", "GET", "http://crm.internal:8080", "/customers/{id}", int64(1500 * time.Millisecond)},
		{"notify-post", "POST", "https://notify.internal", "/v1/send", int64(30 * time.Second)},
		{"tender-update", "PUT", "http://tender-service.internal", "/tenders/{id}", int64(7 * time.Second)},
	}, got)
}

func TestConnectorAliasList_DatabaseUnreachable_Returns503WithoutDriverText(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T) *httptest.ResponseRecorder
	}{
		{
			name: "pool closed",
			run: func(t *testing.T) *httptest.ResponseRecorder {
				pool := newTestPool(t)
				pool.Close()
				return sgCAliasServe(t, pool, http.MethodGet, "/api/v1/internal/connector-aliases")
			},
		},
		{
			name: "query cancelled by the statement timeout while the table is locked",
			run: func(t *testing.T) *httptest.ResponseRecorder {
				adminPool, dsn := fixtures.NewTestPoolAndDSN(t)
				timed, err := pgcommon.NewPool(context.Background(), pgcommon.Config{
					DSN: dsn + "&options=-c%20statement_timeout%3D300ms",
				})
				require.NoError(t, err)
				t.Cleanup(timed.Close)

				var w *httptest.ResponseRecorder
				err = adminPool.WithTx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
					if _, err := tx.Exec(ctx, `LOCK TABLE connector_rest_alias IN ACCESS EXCLUSIVE MODE`); err != nil {
						return err
					}
					w = sgCAliasServe(t, timed, http.MethodGet, "/api/v1/internal/connector-aliases")
					return nil
				})
				require.NoError(t, err)
				return w
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := tc.run(t)

			require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
			var prob map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &prob))
			assert.Equal(t, "UPSTREAM_UNAVAILABLE", prob["code"])
			assert.Equal(t, "an upstream dependency is temporarily unavailable", prob["detail"])
			assert.NotContains(t, prob, "restCall")
			assert.NotContains(t, w.Body.String(), "pool")
			assert.NotContains(t, w.Body.String(), "statement timeout")
		})
	}
}
