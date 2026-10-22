package service_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type loggedLine struct {
	msg    string
	fields map[string]any
}

type recordingLogger struct {
	mu    sync.Mutex
	infos []loggedLine
}

func (l *recordingLogger) Info(msg string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.infos = append(l.infos, loggedLine{msg, fields})
}
func (*recordingLogger) Debug(string, map[string]any) {}
func (*recordingLogger) Warn(string, map[string]any)  {}
func (*recordingLogger) Error(string, map[string]any) {}

func (l *recordingLogger) only(t *testing.T) loggedLine {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	require.Len(t, l.infos, 1)
	return l.infos[0]
}

func TestConnectorService_WritesLeaveAnAuditLineWithoutTheValue(t *testing.T) {
	tenant, user := uuid.New(), uuid.New()
	const secret = "sg-live-do-not-log"

	t.Run("credential write", func(t *testing.T) {
		log := &recordingLogger{}
		svc := service.NewConnectorService(service.ConnectorDeps{Secrets: &fakeSecretsClient{}, Log: log})
		_, err := svc.WriteCredential(context.Background(), tenant, user, "send-email", "apiKey", secret)
		require.NoError(t, err)
		line := log.only(t)
		assert.Equal(t, "connector credential written", line.msg)
		assert.Equal(t, tenant.String(), line.fields["tenant_id"])
		assert.Equal(t, user.String(), line.fields["user_id"])
		assert.Equal(t, "send-email", line.fields["connector_type"])
		assert.Equal(t, "apiKey", line.fields["field_name"])
		assert.NotContains(t, fmt.Sprint(line.fields), secret)
		assert.Contains(t, line.fields, "trace_id")
	})

	t.Run("credential revoke", func(t *testing.T) {
		log := &recordingLogger{}
		svc := service.NewConnectorService(service.ConnectorDeps{Secrets: &fakeSecretsClient{}, Log: log})
		require.NoError(t, svc.RevokeCredential(context.Background(), tenant, user, "send-email", "apiKey"))
		line := log.only(t)
		assert.Equal(t, "connector credential revoked", line.msg)
		assert.Equal(t, user.String(), line.fields["user_id"])
	})

	t.Run("a refused write is not logged as done", func(t *testing.T) {
		log := &recordingLogger{}
		svc := service.NewConnectorService(service.ConnectorDeps{Secrets: &fakeSecretsClient{}, Log: log})
		_, err := svc.WriteCredential(context.Background(), tenant, user, "not-a-connector", "apiKey", secret)
		require.Error(t, err)
		assert.Empty(t, log.infos)
	})

	t.Run("alias write and delete", func(t *testing.T) {
		log := &recordingLogger{}
		svc := service.NewConnectorService(service.ConnectorDeps{Aliases: &fakeAliasRepo{}, Log: log, AliasAllowedHosts: []string{"crm.internal"}})
		require.NoError(t, svc.WriteRestAlias(context.Background(), domain.ConnectorRestAlias{
			Alias: "crm", Method: "POST", BaseURL: "http://crm.internal", PathTemplate: "/v1/x",
		}))
		_, err := svc.DeleteRestAlias(context.Background(), "crm")
		require.NoError(t, err)
		require.Len(t, log.infos, 2)
		assert.Equal(t, "connector alias written", log.infos[0].msg)
		assert.Equal(t, "crm", log.infos[0].fields["alias"])
		assert.Equal(t, "connector alias deleted", log.infos[1].msg)
		assert.True(t, strings.Contains(fmt.Sprint(log.infos[1].fields), "crm"))
	})
}
