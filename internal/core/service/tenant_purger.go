package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-connectors/v2/pkg/registry"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

const tenantsPerPurgeTick = 100

type TenantPurgerDeps struct {
	Retention port.TenantRetentionRepository
	Data      port.TenantDataPurger
	Secrets   port.SecretsClient
	Batch     int
	Interval  time.Duration
	Now       func() time.Time
	Record    func(table string, deleted int64)
	Failed    func()
	Logger    port.Logger
}

type TenantPurger struct {
	deps TenantPurgerDeps
	log  port.Logger
}

func NewTenantPurger(deps TenantPurgerDeps) *TenantPurger {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &TenantPurger{deps: deps, log: logOrNoop(deps.Logger)}
}

func (p *TenantPurger) Tick(ctx context.Context) error {
	due, err := p.deps.Retention.ListDue(ctx, p.deps.Now(), tenantsPerPurgeTick)
	if err != nil {
		return fmt.Errorf("list tenants due for purge: %w", err)
	}
	var failures []error
	for _, tenantID := range due {
		if err := p.purgeTenant(ctx, tenantID); err != nil {
			p.deps.Failed()
			p.log.Error("tenant purge failed", map[string]any{"tenant_id": tenantID.String(), "error": err.Error()})
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (p *TenantPurger) purgeTenant(ctx context.Context, tenantID uuid.UUID) error {
	if err := p.purgeCredentials(ctx, tenantID); err != nil {
		return fmt.Errorf("purge tenant %s: %w", tenantID, err)
	}
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("purge tenant %s: %w", tenantID, err)
		}
		deleted, err := p.deps.Data.PurgeBatch(ctx, tenantID, p.deps.Batch)
		if err != nil {
			return fmt.Errorf("purge tenant %s: %w", tenantID, err)
		}
		if len(deleted) == 0 {
			break
		}
		for table, n := range deleted {
			p.deps.Record(table, n)
		}
	}
	if err := p.deps.Retention.MarkPurged(ctx, tenantID, p.deps.Now()); err != nil {
		return fmt.Errorf("mark tenant %s purged: %w", tenantID, err)
	}
	p.log.Info("tenant purged", map[string]any{"tenant_id": tenantID.String()})
	return nil
}

func (p *TenantPurger) purgeCredentials(ctx context.Context, tenantID uuid.UUID) error {
	if p.deps.Secrets == nil {
		return nil
	}
	for _, path := range tenantCredentialPaths(tenantID) {
		if err := p.deps.Secrets.Delete(ctx, path); err != nil {
			return fmt.Errorf("delete credential %s: %w", path, err)
		}
	}
	return nil
}

func tenantCredentialPaths(tenantID uuid.UUID) []string {
	var paths []string
	for connectorType, def := range registry.All() {
		for _, f := range def.Inputs {
			if f.IsSecretRef() {
				paths = append(paths, fmt.Sprintf("connectors/%s/%s/%s", tenantID, connectorType, f.Name))
			}
		}
	}
	sort.Strings(paths)
	return paths
}

func (p *TenantPurger) Run(ctx context.Context) {
	ticker := time.NewTicker(p.deps.Interval)
	defer ticker.Stop()
	for {
		if err := p.Tick(ctx); err != nil {
			p.log.Error("tenant purge sweep failed", map[string]any{"error": err.Error()})
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
