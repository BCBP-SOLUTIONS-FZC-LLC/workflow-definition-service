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
	for _, d := range due {
		for _, err := range []error{p.purgeCredentials(ctx, d), p.purgeArtifacts(ctx, d)} {
			if err != nil {
				p.deps.Failed()
				p.log.Error("tenant purge failed", map[string]any{"tenant_id": d.TenantID.String(), "error": err.Error()})
				failures = append(failures, err)
			}
		}
	}
	return errors.Join(failures...)
}

func (p *TenantPurger) purgeArtifacts(ctx context.Context, d port.TenantPurgeDue) error {
	if !d.Artifacts {
		return nil
	}
	tenantID := d.TenantID
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("purge tenant %s: %w", tenantID, err)
		}
		deleted, err := p.deps.Data.PurgeBatch(ctx, tenantID, p.deps.Batch)
		if err != nil {
			return fmt.Errorf("purge tenant %s: %w", tenantID, err)
		}
		if len(deleted) == 0 {
			remaining, err := p.deps.Data.HasRows(ctx, tenantID)
			if err != nil {
				return fmt.Errorf("check tenant %s for remaining rows: %w", tenantID, err)
			}
			if remaining {
				return fmt.Errorf("purge tenant %s: rows remain after a pass that deleted none, another replica may be purging it", tenantID)
			}
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

func (p *TenantPurger) purgeCredentials(ctx context.Context, d port.TenantPurgeDue) error {
	if !d.Credentials {
		return nil
	}
	if p.deps.Secrets != nil {
		for _, path := range tenantCredentialPaths(d.TenantID) {
			if err := p.deps.Secrets.Delete(ctx, path); err != nil {
				return fmt.Errorf("purge tenant %s: delete credential %s: %w", d.TenantID, path, err)
			}
		}
	}
	if err := p.deps.Retention.MarkCredentialsPurged(ctx, d.TenantID, p.deps.Now()); err != nil {
		return fmt.Errorf("mark tenant %s credentials purged: %w", d.TenantID, err)
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
