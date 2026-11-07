package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestConnectorService_Credentials_FieldNameIsCappedAt128Characters(t *testing.T) {
	t.Parallel()

	tenant := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	user := uuid.MustParse("22222222-2222-2222-2222-222222222222")
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
	lengths := []struct {
		name      string
		length    int
		wantInput bool
	}{
		{"128 characters is accepted", 128, false},
		{"129 characters is refused", 129, true},
		{"a very long value is refused", 100000, true},
	}
	for _, op := range ops {
		for _, l := range lengths {
			t.Run(op.name+" "+l.name, func(t *testing.T) {
				t.Parallel()

				secrets := &coreSvcASecrets{}
				svc := service.NewConnectorService(service.ConnectorDeps{Secrets: secrets})

				err := op.call(svc, strings.Repeat("a", l.length))

				if !l.wantInput {
					require.NoError(t, err)
					assert.Equal(t, 1, secrets.calls)
					return
				}
				require.Error(t, err)
				assert.True(t, errors.Is(err, domain.ErrInvalidConnectorCredentialInput))
				assert.ErrorContains(t, err, "128")
				assert.Zero(t, secrets.calls, "the secrets store is never reached")
			})
		}
	}
}
