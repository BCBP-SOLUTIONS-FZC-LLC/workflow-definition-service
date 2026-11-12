package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type sgCCountingPinger struct{ calls atomic.Int32 }

func (p *sgCCountingPinger) Ping(context.Context) error {
	p.calls.Add(1)
	return nil
}

type sgCCountingDB struct{ calls atomic.Int32 }

func (d *sgCCountingDB) Health(context.Context) DBHealth {
	d.calls.Add(1)
	return DBHealth{Healthy: true}
}

type sgCAliasRepo struct {
	deletes []string
}

func (*sgCAliasRepo) ListRest(context.Context) ([]domain.ConnectorRestAlias, error) {
	return nil, nil
}
func (*sgCAliasRepo) UpsertRest(context.Context, domain.ConnectorRestAlias) error { return nil }
func (r *sgCAliasRepo) DeleteRest(_ context.Context, alias string) (bool, error) {
	r.deletes = append(r.deletes, alias)
	return true, nil
}

func sgCNewRouter(db DBPinger, cache Pinger, h *handler.Handler) *Router {
	gin.SetMode(gin.TestMode)
	return NewRouter(RouterConfig{
		GinConfig: testGinConfig,
		AppEnv:    "test",
		Handler:   h,
		DB:        db,
		Cache:     cache,
	})
}

func TestRouter_UnroutedPath_ProblemBodyCarriesTypeTitleDetailAndInstance(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		target       string
		wantInstance string
	}{
		{name: "unknown api path", target: "/api/v1/no-such-path", wantInstance: "/api/v1/no-such-path"},
		{name: "unknown root path", target: "/no-such-path", wantInstance: "/no-such-path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := sgCNewRouter(&sgCCountingDB{}, &sgCCountingPinger{}, handler.New(handler.Services{}))
			w := httptest.NewRecorder()

			r.engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.target, nil))

			require.Equal(t, http.StatusNotFound, w.Code)
			assert.Equal(t, "application/problem+json", w.Header().Get("Content-Type"))
			var body map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			assert.Equal(t, "https://api.workflow.platform/errors/not-found", body["type"])
			assert.Equal(t, "Resource Not Found", body["title"])
			assert.EqualValues(t, http.StatusNotFound, body["status"])
			assert.Equal(t, "no route matches this method and path", body["detail"])
			assert.Equal(t, tc.wantInstance, body["instance"])
			assert.Equal(t, "NOT_FOUND", body["code"])
		})
	}
}

func TestRouter_Healthz_AnswersOkJSONWithoutCredentialsAndWithoutTouchingDependencies(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		drain    bool
		withAuth bool
	}{
		{name: "no headers at all"},
		{name: "a stray caller header is ignored", withAuth: true},
		{name: "still ok while the pod drains", drain: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db, cache := &sgCCountingDB{}, &sgCCountingPinger{}
			r := sgCNewRouter(db, cache, handler.New(handler.Services{}))
			if tc.drain {
				r.BeginDrain()
			}
			httpReq := httptest.NewRequest(http.MethodGet, "/healthz", nil)
			if tc.withAuth {
				httpReq.Header.Set("x-user-id", "someone")
			}
			w := httptest.NewRecorder()

			r.engine.ServeHTTP(w, httpReq)

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			assert.Contains(t, w.Header().Get("Content-Type"), "application/json")
			assert.JSONEq(t, `{"status":"ok"}`, w.Body.String())
			assert.Equal(t, `{"status":"ok"}`, w.Body.String(), "the status value is lower case")
			assert.Zero(t, db.calls.Load(), "healthz does not ask the database")
			assert.Zero(t, cache.calls.Load(), "healthz does not ask the cache")
		})
	}
}

func TestRouter_DeleteAliasWithEncodedSlash_IsNotFoundAndNeverReachesTheStore(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		target string
	}{
		{name: "encoded slash after dot dot", target: "/api/v1/internal/connector-aliases/rest/..%2Fx"},
		{name: "encoded slash inside the alias", target: "/api/v1/internal/connector-aliases/rest/tender%2Fget"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo := &sgCAliasRepo{}
			svc := service.NewConnectorService(service.ConnectorDeps{Aliases: repo})
			r := sgCNewRouter(&sgCCountingDB{}, &sgCCountingPinger{}, handler.New(handler.Services{Connectors: svc}))
			httpReq := httptest.NewRequest(http.MethodDelete, tc.target, nil)
			httpReq.Header.Set("x-user-id", "iam-system")
			httpReq.Header.Set("x-tenant-roles", "iam-system")
			w := httptest.NewRecorder()

			r.engine.ServeHTTP(w, httpReq)

			require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
			assert.Equal(t, "application/problem+json", w.Header().Get("Content-Type"))
			var body map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			assert.Equal(t, "NOT_FOUND", body["code"])
			assert.Equal(t, "no route matches this method and path", body["detail"])
			assert.Empty(t, repo.deletes, "no delete reaches the alias store")
		})
	}
}
