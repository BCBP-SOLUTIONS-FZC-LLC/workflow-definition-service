package service_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type sgCAuditLine struct {
	msg    string
	fields map[string]any
}

type sgCAuditLog struct {
	mu    sync.Mutex
	lines []sgCAuditLine
}

func (l *sgCAuditLog) Info(msg string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, sgCAuditLine{msg: msg, fields: fields})
}
func (*sgCAuditLog) Debug(string, map[string]any) {}
func (*sgCAuditLog) Warn(string, map[string]any)  {}
func (*sgCAuditLog) Error(string, map[string]any) {}
func (*sgCAuditLog) Fatal(string, map[string]any) {}

func (l *sgCAuditLog) snapshot() []sgCAuditLine {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]sgCAuditLine(nil), l.lines...)
}

type sgCAliasRepo struct {
	found bool
	err   error
	asked []string
}

func (*sgCAliasRepo) ListRest(context.Context) ([]domain.ConnectorRestAlias, error) {
	return nil, nil
}

func (*sgCAliasRepo) UpsertRest(context.Context, domain.ConnectorRestAlias) error { return nil }

func (r *sgCAliasRepo) DeleteRest(_ context.Context, alias string) (bool, error) {
	r.asked = append(r.asked, alias)
	return r.found, r.err
}

func sgCTracedContext(t *testing.T) (context.Context, string) {
	t.Helper()
	traceID, err := trace.TraceIDFromHex("a1a2a3a4a5a6a7a8a9aaabacadaeaf10")
	require.NoError(t, err)
	spanID, err := trace.SpanIDFromHex("b1b2b3b4b5b6b7b8")
	require.NoError(t, err)
	sc := trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled})
	return trace.ContextWithSpanContext(context.Background(), sc), traceID.String()
}

func TestConnectorService_DeleteRestAlias_AuditLine_RecordsAliasFoundAndTraceID(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		found     bool
		repoErr   error
		wantFound bool
		wantLine  bool
	}{
		{name: "alias found and deleted", found: true, wantFound: true, wantLine: true},
		{name: "repository error writes no line", repoErr: errors.New("alias table unreachable"), wantLine: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx, traceID := sgCTracedContext(t)
			log := &sgCAuditLog{}
			repo := &sgCAliasRepo{found: tc.found, err: tc.repoErr}
			svc := service.NewConnectorService(service.ConnectorDeps{Aliases: repo, Log: log})

			found, err := svc.DeleteRestAlias(ctx, "tender-get")

			assert.Equal(t, []string{"tender-get"}, repo.asked)
			lines := log.snapshot()
			if !tc.wantLine {
				require.ErrorIs(t, err, tc.repoErr)
				assert.False(t, found)
				assert.Empty(t, lines)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantFound, found)
			require.Len(t, lines, 1)
			assert.Equal(t, "connector alias deleted", lines[0].msg)
			assert.Equal(t, map[string]any{
				"alias":    "tender-get",
				"found":    tc.wantFound,
				"trace_id": traceID,
			}, lines[0].fields)
		})
	}
}
