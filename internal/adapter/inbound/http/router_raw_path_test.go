package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
)

type recordingDeleteConnectorFake struct {
	wiringConnectorFake
	deleted []string
}

func (f *recordingDeleteConnectorFake) DeleteRestAlias(_ context.Context, alias string) (bool, error) {
	f.deleted = append(f.deleted, alias)
	return true, nil
}

func newSystemCallerRequest(method, target string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	req.Header.Set("x-user-id", "iam-system")
	req.Header.Set("x-tenant-roles", "iam-system")
	return req
}

func TestRouter_EncodedSlashInTheInternalPrefix_ReachesNoHandler(t *testing.T) {
	// The mesh policy matches the raw path; routing on the decoded path would let
	// /api/v1/internal%2Fconnector-aliases reach an internal handler as a path the
	// policy's first rule admits for every caller.
	for _, target := range []string{
		"/api/v1/internal%2Fconnector-aliases",
		"/api/v1/internal%2fconnector-aliases",
		"/api/v1%2Finternal/connector-aliases",
		"/api%2Fv1/internal/connector-aliases",
	} {
		t.Run(target, func(t *testing.T) {
			conn := &wiringConnectorFake{}
			engine := newWiringEngine(t, "test", RequestTimeout, conn)

			w := httptest.NewRecorder()
			engine.ServeHTTP(w, newSystemCallerRequest(http.MethodGet, target))

			assert.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
			assert.Zero(t, conn.listCalls, "no handler may run")
		})
	}
}

func TestRouter_EncodedLetterInTheInternalPrefix_ReachesNoHandler(t *testing.T) {
	conn := &wiringConnectorFake{}
	engine := newWiringEngine(t, "test", RequestTimeout, conn)

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, newSystemCallerRequest(http.MethodGet, "/api/v1/%69nternal/connector-aliases"))

	assert.Equal(t, http.StatusNotFound, w.Code, "the mesh sees /api/v1/%69nternal, which no policy rule names as internal")
	assert.Zero(t, conn.listCalls)
}

func TestRouter_PlainInternalRequest_StillReachesItsHandler(t *testing.T) {
	conn := &wiringConnectorFake{}
	engine := newWiringEngine(t, "test", RequestTimeout, conn)

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, newSystemCallerRequest(http.MethodGet, "/api/v1/internal/connector-aliases"))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, 1, conn.listCalls)
}

func TestRouter_EncodedCharacterInAPathParameter_ArrivesDecoded(t *testing.T) {
	cases := map[string]string{
		"/api/v1/internal/connector-aliases/rest/billing":      "billing",
		"/api/v1/internal/connector-aliases/rest/bill%2Ding":   "bill-ing",
		"/api/v1/internal/connector-aliases/rest/bill%5Fing":   "bill_ing",
		"/api/v1/internal/connector-aliases/rest/bill%252Ding": "bill%2Ding",
	}
	for target, want := range cases {
		t.Run(target, func(t *testing.T) {
			conn := &recordingDeleteConnectorFake{}
			engine := NewRouter(RouterConfig{
				GinConfig: testGinConfig,
				AppEnv:    "test",
				Handler:   handler.New(handler.Services{Connectors: conn}),
				DB:        fakeDBPinger{health: DBHealth{Healthy: true}},
				Cache:     fakePinger{},
			}).engine

			w := httptest.NewRecorder()
			engine.ServeHTTP(w, newSystemCallerRequest(http.MethodDelete, target))

			require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
			assert.Equal(t, []string{want}, conn.deleted, "the parameter is unescaped once, as before")
		})
	}
}
