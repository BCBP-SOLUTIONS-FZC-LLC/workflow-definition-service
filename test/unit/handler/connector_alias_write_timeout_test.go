package handler_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestWriteConnectorRestAlias_TimeoutOfWrongJSONType_Returns400(t *testing.T) {
	cases := []struct {
		name      string
		timeoutMs any
	}{
		{name: "string", timeoutMs: "5"},
		{name: "fractional number", timeoutMs: 1.5},
		{name: "boolean", timeoutMs: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			called := false
			conn := &fakeConnectorSvc{
				writeRestFn: func(context.Context, domain.ConnectorRestAlias) error {
					called = true
					return nil
				},
			}
			body := map[string]any{
				"alias": "tender-get", "method": "GET",
				"baseURL": "http://tender-service.internal", "pathTemplate": "/x",
				"timeoutMs": tc.timeoutMs,
			}

			w := do(aliasRouter(handler.New(handler.Services{Connectors: conn})),
				req(http.MethodPost, "/api/v1/internal/connector-aliases/rest", body))

			require.Equal(t, http.StatusBadRequest, w.Code)
			prob := decodeProblem(t, w)
			assert.True(t, strings.HasSuffix(prob["type"].(string), "/errors/bad-request"), "type = %v", prob["type"])
			assert.False(t, called, "a body that fails to bind must not reach the service")
		})
	}
}

func TestWriteConnectorRestAlias_TimeoutBoundaries_PassedToServiceAsDuration(t *testing.T) {
	cases := []struct {
		name        string
		timeoutMs   int64
		wantTimeout time.Duration
	}{
		{name: "one millisecond is kept", timeoutMs: 1, wantTimeout: time.Millisecond},
		{name: "maximum is kept", timeoutMs: 60000, wantTimeout: time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var got domain.ConnectorRestAlias
			conn := &fakeConnectorSvc{
				writeRestFn: func(_ context.Context, a domain.ConnectorRestAlias) error {
					got = a
					return nil
				},
			}
			body := map[string]any{
				"alias": "tender-get", "method": "GET",
				"baseURL": "http://tender-service.internal", "pathTemplate": "/x",
				"timeoutMs": tc.timeoutMs,
			}

			w := do(aliasRouter(handler.New(handler.Services{Connectors: conn})),
				req(http.MethodPost, "/api/v1/internal/connector-aliases/rest", body))

			require.Equal(t, http.StatusNoContent, w.Code)
			assert.Empty(t, w.Body.String())
			assert.Equal(t, domain.ConnectorRestAlias{
				Alias: "tender-get", Method: "GET", BaseURL: "http://tender-service.internal",
				PathTemplate: "/x", Timeout: tc.wantTimeout,
			}, got)
		})
	}
}
