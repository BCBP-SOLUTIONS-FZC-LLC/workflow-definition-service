package handler_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

const w6aAliasWritePath = "/api/v1/internal/connector-aliases/rest"

type w6aAliasStore struct {
	upserted []domain.ConnectorRestAlias
}

func (s *w6aAliasStore) ListRest(context.Context) ([]domain.ConnectorRestAlias, error) {
	return s.upserted, nil
}

func (s *w6aAliasStore) UpsertRest(_ context.Context, a domain.ConnectorRestAlias) error {
	s.upserted = append(s.upserted, a)
	return nil
}

func (s *w6aAliasStore) DeleteRest(context.Context, string) (bool, error) { return false, nil }

type w6aLogLine struct {
	msg    string
	fields map[string]any
}

type w6aLogRecorder struct {
	infos []w6aLogLine
}

func (l *w6aLogRecorder) Info(msg string, fields map[string]any) {
	l.infos = append(l.infos, w6aLogLine{msg, fields})
}
func (*w6aLogRecorder) Debug(string, map[string]any) {}
func (*w6aLogRecorder) Warn(string, map[string]any)  {}
func (*w6aLogRecorder) Error(string, map[string]any) {}
func (*w6aLogRecorder) Fatal(string, map[string]any) {}

func w6aAliasBody(timeoutMs any) map[string]any {
	return map[string]any{
		"alias": "tender-get", "method": "GET",
		"baseURL": "http://tender-service.internal", "pathTemplate": "/x",
		"timeoutMs": timeoutMs,
	}
}

func TestWriteConnectorRestAlias_TimeoutOverTheLimitThroughTheService_400InvalidInputNamingTheBounds(t *testing.T) {
	t.Parallel()

	for _, timeoutMs := range []int64{60001, 60002, 3600000} {
		t.Run(fmt.Sprint(timeoutMs), func(t *testing.T) {
			t.Parallel()
			store := &w6aAliasStore{}
			svc := service.NewConnectorService(service.ConnectorDeps{
				Aliases: store, AliasAllowedHosts: []string{".internal"}, Log: &w6aLogRecorder{},
			})

			w := do(aliasRouter(handler.New(handler.Services{Connectors: svc})),
				req(http.MethodPost, w6aAliasWritePath, w6aAliasBody(timeoutMs)))

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, "https://api.workflow.platform/errors/bad-request", prob["type"])
			assert.Equal(t, "INVALID_INPUT", prob["code"])
			assert.Equal(t, "invalid connector alias input: timeoutMs must be between 0 and 60000", prob["detail"])
			assert.Empty(t, store.upserted, "a refused alias is not stored")
		})
	}
}

func TestWriteConnectorRestAlias_ValidAlias_204NoBodyStoresTheRowAndLogsItWithTheTraceID(t *testing.T) {
	t.Parallel()

	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	require.NoError(t, err)
	spanCtx := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8}, TraceFlags: trace.FlagsSampled,
	})
	store := &w6aAliasStore{}
	log := &w6aLogRecorder{}
	svc := service.NewConnectorService(service.ConnectorDeps{
		Aliases: store, AliasAllowedHosts: []string{".internal"}, Log: log,
	})
	httpReq := req(http.MethodPost, w6aAliasWritePath, w6aAliasBody(2500))
	httpReq = httpReq.WithContext(trace.ContextWithSpanContext(httpReq.Context(), spanCtx))

	w := do(aliasRouter(handler.New(handler.Services{Connectors: svc})), httpReq)

	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	assert.Zero(t, w.Body.Len())
	assert.Equal(t, []domain.ConnectorRestAlias{{
		Alias: "tender-get", Method: "GET", BaseURL: "http://tender-service.internal",
		PathTemplate: "/x", Timeout: 2500 * time.Millisecond,
	}}, store.upserted)
	require.Len(t, log.infos, 1)
	assert.Equal(t, "connector alias written", log.infos[0].msg)
	assert.Equal(t, "tender-get", log.infos[0].fields["alias"])
	assert.Equal(t, "GET", log.infos[0].fields["method"])
	assert.Equal(t, "http://tender-service.internal", log.infos[0].fields["base_url"])
	assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", log.infos[0].fields["trace_id"])
}

func TestWriteConnectorRestAlias_BodyThatIsNotJSON_400BadRequestWarnedAndNotForwarded(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{name: "an opening brace only", body: "{"},
		{name: "empty body", body: ""},
		{name: "only white space", body: "  \n"},
		{name: "plain text", body: "alias=tender-get"},
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
			log := &fakeLogger{}
			httpReq := httptest.NewRequest(http.MethodPost, w6aAliasWritePath, strings.NewReader(tc.body))
			httpReq.Header.Set("Content-Type", "application/json")

			w := do(aliasRouter(handler.New(handler.Services{Connectors: conn, Log: log})), httpReq)

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, "https://api.workflow.platform/errors/bad-request", prob["type"])
			assert.Equal(t, "BAD_REQUEST", prob["code"])
			assert.Equal(t, "request body is not valid JSON", prob["detail"])
			assert.False(t, called, "a body that cannot be read must not reach the service")
			assert.Equal(t, []string{"invalid request body"}, log.warnCalls)
			assert.Empty(t, log.errorCalls)
		})
	}
}

func TestWriteConnectorRestAlias_TimeoutOfWrongJSONType_400NamesTheFieldAndIsWarned(t *testing.T) {
	t.Parallel()

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
			log := &fakeLogger{}

			w := do(aliasRouter(handler.New(handler.Services{Connectors: conn, Log: log})),
				req(http.MethodPost, w6aAliasWritePath, w6aAliasBody(tc.timeoutMs)))

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, "https://api.workflow.platform/errors/bad-request", prob["type"])
			assert.Equal(t, "BAD_REQUEST", prob["code"])
			assert.Equal(t, "request body has a field of the wrong type", prob["detail"])
			assert.Equal(t, []any{map[string]any{"name": "timeoutMs", "reason": "has the wrong type"}}, prob["invalid_params"])
			assert.False(t, called, "a body that fails to bind must not reach the service")
			assert.Equal(t, []string{"invalid request body"}, log.warnCalls)
		})
	}
}
