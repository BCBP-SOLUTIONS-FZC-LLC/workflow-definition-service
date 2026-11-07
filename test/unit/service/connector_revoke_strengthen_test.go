package service_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type sgDInfoLine struct {
	msg    string
	fields map[string]any
}

type sgDLogger struct {
	mu    sync.Mutex
	lines []sgDInfoLine
}

func (l *sgDLogger) Info(msg string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, sgDInfoLine{msg: msg, fields: fields})
}
func (*sgDLogger) Debug(string, map[string]any) {}
func (*sgDLogger) Warn(string, map[string]any)  {}
func (*sgDLogger) Error(string, map[string]any) {}

func (l *sgDLogger) snapshot() []sgDInfoLine {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]sgDInfoLine(nil), l.lines...)
}

type sgDSecrets struct {
	mu      sync.Mutex
	deleted []string
	err     error
}

func (*sgDSecrets) Write(context.Context, string, map[string]string) error { return nil }

func (s *sgDSecrets) Delete(_ context.Context, path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleted = append(s.deleted, path)
	return s.err
}

func (s *sgDSecrets) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.deleted...)
}

func sgDTracedContext(t *testing.T) (context.Context, string) {
	t.Helper()
	traceID, err := trace.TraceIDFromHex("0102030405060708090a0b0c0d0e0f10")
	require.NoError(t, err)
	spanID, err := trace.SpanIDFromHex("1112131415161718")
	require.NoError(t, err)
	sc := trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled})
	return trace.ContextWithSpanContext(context.Background(), sc), traceID.String()
}

func TestConnectorService_RevokeCredential_AcceptsEveryAllowedFieldNameCharacter(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		fieldName string
	}{
		{name: "mixed case with underscore hyphen and digit", fieldName: "Api_Key-2"},
		{name: "digits only", fieldName: "0123456789"},
		{name: "hyphens only", fieldName: "---"},
		{name: "underscores only", fieldName: "___"},
		{name: "single character", fieldName: "x"},
		{name: "upper case only", fieldName: "ABCXYZ"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			secrets := &sgDSecrets{}
			svc := service.NewConnectorService(service.ConnectorDeps{Secrets: secrets})
			tenant := uuid.New()

			require.NoError(t, svc.RevokeCredential(context.Background(), tenant, uuid.New(), "send-email", tc.fieldName))

			assert.Equal(t, []string{"connectors/" + tenant.String() + "/send-email/" + tc.fieldName}, secrets.paths())
		})
	}
}

func TestConnectorService_RevokeCredential_AddressesOnlyTheCallersTenantSubtree(t *testing.T) {
	t.Parallel()

	tenantA, tenantB := uuid.New(), uuid.New()
	secrets := &sgDSecrets{}
	svc := service.NewConnectorService(service.ConnectorDeps{Secrets: secrets})

	require.NoError(t, svc.RevokeCredential(context.Background(), tenantA, uuid.New(), "send-email", "apiKey"))
	require.NoError(t, svc.RevokeCredential(context.Background(), tenantB, uuid.New(), "send-email", "apiKey"))

	assert.Equal(t, []string{
		"connectors/" + tenantA.String() + "/send-email/apiKey",
		"connectors/" + tenantB.String() + "/send-email/apiKey",
	}, secrets.paths())
}

func TestConnectorService_RevokeCredential_AuditLineCarriesEveryField(t *testing.T) {
	t.Parallel()

	ctx, wantTrace := sgDTracedContext(t)
	tenant, user := uuid.New(), uuid.New()
	log := &sgDLogger{}
	svc := service.NewConnectorService(service.ConnectorDeps{Secrets: &sgDSecrets{}, Log: log})

	require.NoError(t, svc.RevokeCredential(ctx, tenant, user, "rest-call", "token_2"))

	lines := log.snapshot()
	require.Len(t, lines, 1)
	assert.Equal(t, "connector credential revoked", lines[0].msg)
	assert.Equal(t, map[string]any{
		"tenant_id":      tenant.String(),
		"user_id":        user.String(),
		"connector_type": "rest-call",
		"field_name":     "token_2",
		"trace_id":       wantTrace,
	}, lines[0].fields)
}

func TestConnectorService_RevokeCredential_LogsNothingWhenRefused(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		connectorType string
		fieldName     string
		secrets       func() *sgDSecrets
		wantDeletes   int
	}{
		{name: "unknown connector type", connectorType: "not-a-connector", fieldName: "apiKey", secrets: func() *sgDSecrets { return &sgDSecrets{} }},
		{name: "field name outside the allowlist", connectorType: "send-email", fieldName: "a/b", secrets: func() *sgDSecrets { return &sgDSecrets{} }},
		{name: "secrets store fails", connectorType: "send-email", fieldName: "apiKey", secrets: func() *sgDSecrets { return &sgDSecrets{err: errors.New("store down")} }, wantDeletes: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			log := &sgDLogger{}
			secrets := tc.secrets()
			svc := service.NewConnectorService(service.ConnectorDeps{Secrets: secrets, Log: log})

			require.Error(t, svc.RevokeCredential(context.Background(), uuid.New(), uuid.New(), tc.connectorType, tc.fieldName))

			assert.Empty(t, log.snapshot())
			assert.Len(t, secrets.paths(), tc.wantDeletes)
		})
	}
}
