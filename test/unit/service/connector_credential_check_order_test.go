package service_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type coreSvcASecrets struct {
	err   error
	calls int
}

func (s *coreSvcASecrets) Write(context.Context, string, map[string]string) error {
	s.calls++
	return s.err
}

func (s *coreSvcASecrets) Delete(context.Context, string) error {
	s.calls++
	return s.err
}

func TestConnectorService_Credentials_ChecksInputBeforeTheSecretsStoreAndWrapsStoreErrors(t *testing.T) {
	t.Parallel()

	storeDown := errors.New("openbao: 503 sealed")
	cases := []struct {
		name          string
		connectorType string
		fieldName     string
		secrets       *coreSvcASecrets
		wantInput     bool
		wantMessage   string
		wantUpstream  bool
		wantStoreErr  bool
		wantStoreCall bool
	}{
		{
			name: "unknown type and bad field name report the type", connectorType: "not-a-connector", fieldName: "../x",
			secrets: &coreSvcASecrets{}, wantInput: true, wantMessage: "unrecognized connector_type",
		},
		{
			name: "valid type and bad field name report the field name", connectorType: "send-email", fieldName: "a/b",
			secrets: &coreSvcASecrets{}, wantInput: true, wantMessage: "field_name must match",
		},
		{
			name: "valid input without a secrets store is unavailable", connectorType: "send-email", fieldName: "apiKey",
			wantUpstream: true, wantMessage: "OpenBao is not configured",
		},
		{
			name: "a failing secrets store error is wrapped", connectorType: "send-email", fieldName: "apiKey",
			secrets: &coreSvcASecrets{err: storeDown}, wantStoreErr: true, wantStoreCall: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			operations := []struct {
				name       string
				storePrefx string
				run        func(svc *service.ConnectorService) (string, error)
			}{
				{"write", "write credential: ", func(svc *service.ConnectorService) (string, error) {
					return svc.WriteCredential(context.Background(), uuid.New(), uuid.New(), tc.connectorType, tc.fieldName, "value")
				}},
				{"revoke", "revoke credential: ", func(svc *service.ConnectorService) (string, error) {
					return "", svc.RevokeCredential(context.Background(), uuid.New(), uuid.New(), tc.connectorType, tc.fieldName)
				}},
			}
			for _, op := range operations {
				t.Run(op.name, func(t *testing.T) {
					log := &coreSvcALog{}
					deps := service.ConnectorDeps{Log: log}
					if tc.secrets != nil {
						secrets := &coreSvcASecrets{err: tc.secrets.err}
						deps.Secrets = port.SecretsClient(secrets)
						defer func() { assert.Equal(t, tc.wantStoreCall, secrets.calls == 1) }()
					}
					svc := service.NewConnectorService(deps)

					path, err := op.run(svc)

					require.Error(t, err)
					assert.Empty(t, path)
					assert.Equal(t, tc.wantInput, errors.Is(err, domain.ErrInvalidConnectorCredentialInput))
					assert.Equal(t, tc.wantUpstream, errors.Is(err, domain.ErrUpstreamUnavailable))
					assert.Equal(t, tc.wantStoreErr, errors.Is(err, storeDown))
					if tc.wantMessage != "" {
						assert.Contains(t, err.Error(), tc.wantMessage)
					}
					if tc.wantStoreErr {
						assert.Contains(t, err.Error(), op.storePrefx)
					}
					assert.Empty(t, log.infos, "a refused or failed call leaves no audit line")
				})
			}
		})
	}
}

func TestConnectorService_WriteCredential_ABlankValueIsRefusedBeforeTheMissingSecretsStoreIsReported(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"", "   ", "\t\n"} {
		t.Run(fmt.Sprintf("%q", value), func(t *testing.T) {
			t.Parallel()
			svc := service.NewConnectorService(service.ConnectorDeps{Log: &coreSvcALog{}})

			path, err := svc.WriteCredential(context.Background(), uuid.New(), uuid.New(), "send-email", "apiKey", value)

			require.Error(t, err)
			assert.Empty(t, path)
			assert.ErrorIs(t, err, domain.ErrInvalidConnectorCredentialInput, "a bad request is a 400, whether or not the store is configured")
			assert.NotErrorIs(t, err, domain.ErrUpstreamUnavailable)
			assert.ErrorContains(t, err, "value must not be blank")
		})
	}
}
