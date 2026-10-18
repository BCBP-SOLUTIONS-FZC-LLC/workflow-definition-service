package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

type TenantOffboardingDeps struct {
	Retention         port.TenantRetentionRepository
	ArtifactRetention time.Duration
	Now               func() time.Time
}

type TenantOffboardingService struct {
	deps TenantOffboardingDeps
}

func NewTenantOffboardingService(deps TenantOffboardingDeps) *TenantOffboardingService {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &TenantOffboardingService{deps: deps}
}

func (s *TenantOffboardingService) RecordOffboarded(ctx context.Context, tenantID uuid.UUID, at time.Time) error {
	if at.IsZero() {
		at = s.deps.Now()
	}
	if err := s.deps.Retention.RecordOffboarded(ctx, tenantID, at, at.Add(s.deps.ArtifactRetention)); err != nil {
		return fmt.Errorf("record offboarded tenant: %w", err)
	}
	return nil
}
