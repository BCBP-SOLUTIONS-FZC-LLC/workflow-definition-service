package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestConnectorService_Credentials_FieldNameOutsideTheAllowedCharset_IsRefusedBeforeTheSecretsStore(t *testing.T) {
	t.Parallel()

	tenant, user := uuid.New(), uuid.New()
	refused := []struct{ name, field string }{
		{"empty", ""},
		{"dot-dot", ".."},
		{"single dot", "."},
		{"slash", "a/b"},
		{"backslash", `a\b`},
		{"dot inside", "a.b"},
		{"space", "a b"},
		{"trailing newline", "apiKey\n"},
		{"leading newline", "\napiKey"},
		{"tab", "api\tKey"},
		{"nul byte", "api\x00Key"},
		{"unicode letter", "apiKéy"},
		{"unicode digit", "api٣"},
		{"percent sign", "api%2e%2e"},
		{"colon", "a:b"},
	}
	accepted := []struct{ name, field string }{
		{"letters digits dash underscore", "Api_Key-9"},
		{"digits only", "123"},
		{"single dash", "-"},
	}
	ops := []struct {
		name string
		call func(svc *service.ConnectorService, field string) error
	}{
		{"write", func(svc *service.ConnectorService, field string) error {
			_, err := svc.WriteCredential(context.Background(), tenant, user, "send-email", field, "v")
			return err
		}},
		{"revoke", func(svc *service.ConnectorService, field string) error {
			return svc.RevokeCredential(context.Background(), tenant, user, "send-email", field)
		}},
	}
	for _, op := range ops {
		for _, tc := range refused {
			t.Run(op.name+" refuses "+tc.name, func(t *testing.T) {
				t.Parallel()
				secrets := &fakeSecretsClient{}
				svc := service.NewConnectorService(service.ConnectorDeps{Secrets: secrets})

				err := op.call(svc, tc.field)

				require.Error(t, err)
				assert.True(t, errors.Is(err, domain.ErrInvalidConnectorCredentialInput), "got: %v", err)
				assert.Empty(t, secrets.written)
				assert.Empty(t, secrets.deleted)
			})
		}
		for _, tc := range accepted {
			t.Run(op.name+" accepts "+tc.name, func(t *testing.T) {
				t.Parallel()
				secrets := &fakeSecretsClient{}
				svc := service.NewConnectorService(service.ConnectorDeps{Secrets: secrets})

				require.NoError(t, op.call(svc, tc.field))

				want := "connectors/" + tenant.String() + "/send-email/" + tc.field
				assert.Equal(t, []string{want}, append(append([]string{}, secrets.written...), secrets.deleted...))
			})
		}
	}
}
