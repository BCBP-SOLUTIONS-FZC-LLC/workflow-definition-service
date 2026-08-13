package service

import (
	"context"
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-connectors/pkg/registry"
	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

type ConnectorDeps struct {
	Secrets port.SecretsClient
	Log     port.Logger
}

type ConnectorService struct {
	secrets port.SecretsClient
	log     port.Logger
}

func NewConnectorService(d ConnectorDeps) *ConnectorService {
	return &ConnectorService{
		secrets: d.Secrets,
		log:     logOrNoop(d.Log),
	}
}

func (s *ConnectorService) Registry(_ context.Context) map[string]registry.Definition {
	return registry.All()
}

// WriteCredential writes a connector's raw provider credential to OpenBao at
// author time and returns only the resulting secret path — the raw value
// never leaves this call (design/LLD/workflow_connectors.md §6.2).
func (s *ConnectorService) WriteCredential(
	ctx context.Context,
	tenantID uuid.UUID,
	connectorType, fieldName, value string,
) (secretPath string, err error) {
	if s.secrets == nil {
		return "", fmt.Errorf("%w: OpenBao is not configured", domain.ErrUpstreamUnavailable)
	}
	secretPath = fmt.Sprintf("connectors/%s/%s/%s", tenantID, connectorType, fieldName)
	if err := s.secrets.Write(ctx, secretPath, map[string]string{fieldName: value}); err != nil {
		return "", fmt.Errorf("write credential: %w", err)
	}
	return secretPath, nil
}
