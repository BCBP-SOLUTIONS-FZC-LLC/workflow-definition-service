package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-connectors/v2/pkg/registry"
	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

const maxFieldNameLength = 128

var validConnectorSegment = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func validFieldName(fieldName string) bool {
	return len(fieldName) <= maxFieldNameLength && validConnectorSegment.MatchString(fieldName)
}

type ConnectorDeps struct {
	Secrets           port.SecretsClient
	Aliases           port.ConnectorAliasRepository
	Log               port.Logger
	AliasAllowedHosts []string
}

type ConnectorService struct {
	secrets           port.SecretsClient
	aliases           port.ConnectorAliasRepository
	log               port.Logger
	aliasAllowedHosts []string
}

func NewConnectorService(d ConnectorDeps) *ConnectorService {
	return &ConnectorService{
		secrets:           d.Secrets,
		aliases:           d.Aliases,
		log:               logOrNoop(d.Log),
		aliasAllowedHosts: d.AliasAllowedHosts,
	}
}

func (s *ConnectorService) Registry(_ context.Context) map[string]registry.Definition {
	return registry.All()
}

func (s *ConnectorService) WriteCredential(
	ctx context.Context,
	tenantID, userID uuid.UUID,
	connectorType, fieldName, value string,
) (secretPath string, err error) {
	if _, ok := registry.All()[connectorType]; !ok {
		return "", fmt.Errorf("%w: unrecognized connector_type %q", domain.ErrInvalidConnectorCredentialInput, connectorType)
	}
	if !validFieldName(fieldName) {
		return "", fmt.Errorf("%w: field_name must match %s and be at most %d characters", domain.ErrInvalidConnectorCredentialInput, validConnectorSegment.String(), maxFieldNameLength)
	}
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%w: value must not be blank", domain.ErrInvalidConnectorCredentialInput)
	}
	if s.secrets == nil {
		return "", fmt.Errorf("%w: OpenBao is not configured", domain.ErrUpstreamUnavailable)
	}
	secretPath = fmt.Sprintf("connectors/%s/%s/%s", tenantID, connectorType, fieldName)
	if err := s.secrets.Write(ctx, secretPath, map[string]string{fieldName: value}); err != nil {
		return "", fmt.Errorf("write credential: %w", err)
	}
	s.log.Info("connector credential written", auditFields(ctx, tenantID, userID, map[string]any{
		"connector_type": connectorType,
		"field_name":     fieldName,
	}))
	return secretPath, nil
}

func (s *ConnectorService) RevokeCredential(ctx context.Context, tenantID, userID uuid.UUID, connectorType, fieldName string) error {
	if _, ok := registry.All()[connectorType]; !ok {
		return fmt.Errorf("%w: unrecognized connector_type %q", domain.ErrInvalidConnectorCredentialInput, connectorType)
	}
	if !validFieldName(fieldName) {
		return fmt.Errorf("%w: field_name must match %s and be at most %d characters", domain.ErrInvalidConnectorCredentialInput, validConnectorSegment.String(), maxFieldNameLength)
	}
	if s.secrets == nil {
		return fmt.Errorf("%w: OpenBao is not configured", domain.ErrUpstreamUnavailable)
	}
	secretPath := fmt.Sprintf("connectors/%s/%s/%s", tenantID, connectorType, fieldName)
	if err := s.secrets.Delete(ctx, secretPath); err != nil {
		return fmt.Errorf("revoke credential: %w", err)
	}
	s.log.Info("connector credential revoked", auditFields(ctx, tenantID, userID, map[string]any{
		"connector_type": connectorType,
		"field_name":     fieldName,
	}))
	return nil
}

func (s *ConnectorService) ListAliases(ctx context.Context) ([]domain.ConnectorRestAlias, error) {
	return s.aliases.ListRest(ctx)
}

func (s *ConnectorService) WriteRestAlias(ctx context.Context, a domain.ConnectorRestAlias) error {
	if err := a.Validate(); err != nil {
		return err
	}
	if err := domain.ValidateBaseURLHost(a.BaseURL, s.aliasAllowedHosts); err != nil {
		return err
	}
	if err := s.aliases.UpsertRest(ctx, a); err != nil {
		return err
	}
	s.log.Info("connector alias written", stampAudit(ctx, map[string]any{
		"alias":    a.Alias,
		"method":   a.Method,
		"base_url": a.BaseURL,
	}))
	return nil
}

func (s *ConnectorService) DeleteRestAlias(ctx context.Context, alias string) (bool, error) {
	found, err := s.aliases.DeleteRest(ctx, alias)
	if err != nil {
		return false, err
	}
	if found {
		s.log.Info("connector alias deleted", stampAudit(ctx, map[string]any{"alias": alias, "found": found}))
	}
	return found, nil
}
