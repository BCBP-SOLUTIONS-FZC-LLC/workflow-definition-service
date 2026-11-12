package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-connectors/v2/pkg/registry"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

const (
	tenantsPerPurgeTick = 100
	maxPurgeBackoff     = 7 * 24 * time.Hour
	// the failure record is written even when shutdown has cancelled the pass
	recordFailureTimeout = 5 * time.Second
)

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
		if !isStop(ctx, err) {
			p.deps.Failed()
		}
		return fmt.Errorf("list tenants due for purge: %w", err)
	}
	var failures []error
	for _, d := range due {
		failures = append(failures, p.purgeTenant(ctx, d)...)
	}
	return errors.Join(failures...)
}

func (p *TenantPurger) purgeTenant(ctx context.Context, d port.TenantPurgeDue) []error {
	version := d.RecordVersion
	attempts := d.Attempts
	var failures []error
	realFailures := 0
	for _, part := range []func(context.Context, port.TenantPurgeDue, int64) (int64, error){p.purgeCredentials, p.purgeArtifacts} {
		next, err := part(ctx, d, version)
		if errors.Is(err, domain.ErrTenantRetentionChanged) {
			p.log.Info("tenant purge skipped: its record changed", map[string]any{"tenant_id": d.TenantID.String()})
			return nil
		}
		if err != nil && isStop(ctx, err) {
			failures = append(failures, err)
			break
		}
		if err != nil {
			realFailures++
			p.deps.Failed()
			p.log.Error("tenant purge failed", map[string]any{"tenant_id": d.TenantID.String(), "error": err.Error()})
			failures = append(failures, err)
			continue
		}
		if next != version {
			attempts = 0
		}
		version = next
	}
	if realFailures > 0 {
		attempts++
		recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordFailureTimeout)
		defer cancel()
		err := p.deps.Retention.RecordFailure(recordCtx, d.TenantID, version, attempts, p.deps.Now().Add(p.backoff(attempts)))
		if err != nil && !errors.Is(err, domain.ErrTenantRetentionChanged) {
			failures = append(failures, fmt.Errorf("record tenant %s purge failure: %w", d.TenantID, err))
		}
	}
	return failures
}

// isStop reports an error caused by the run's own context ending, as at
// shutdown: it is returned to the caller but is not a purge failure.
func isStop(ctx context.Context, err error) bool {
	return ctx.Err() != nil && errors.Is(err, ctx.Err())
}

func (p *TenantPurger) backoff(attempts int) time.Duration {
	wait := p.deps.Interval
	for i := 1; i < attempts && wait < maxPurgeBackoff; i++ {
		wait *= 2
	}
	return min(wait, maxPurgeBackoff)
}

func (p *TenantPurger) purgeArtifacts(ctx context.Context, d port.TenantPurgeDue, version int64) (int64, error) {
	if !d.Artifacts {
		return version, nil
	}
	tenantID := d.TenantID
	for {
		if err := ctx.Err(); err != nil {
			return version, fmt.Errorf("purge tenant %s: %w", tenantID, err)
		}
		deleted, err := p.deps.Data.PurgeBatch(ctx, tenantID, version, p.deps.Batch)
		if err != nil {
			return version, fmt.Errorf("purge tenant %s: %w", tenantID, err)
		}
		if len(deleted) == 0 {
			remaining, err := p.deps.Data.HasRows(ctx, tenantID)
			if err != nil {
				return version, fmt.Errorf("check tenant %s for remaining rows: %w", tenantID, err)
			}
			if remaining {
				return version, fmt.Errorf("purge tenant %s: rows remain after a pass that deleted none, another replica may be purging it", tenantID)
			}
			break
		}
		for table, n := range deleted {
			p.deps.Record(table, n)
		}
	}
	next, err := p.deps.Retention.MarkPurged(ctx, tenantID, version, p.deps.Now())
	if err != nil {
		return version, fmt.Errorf("mark tenant %s purged: %w", tenantID, err)
	}
	p.log.Info("tenant purged", map[string]any{"tenant_id": tenantID.String()})
	return next, nil
}

func (p *TenantPurger) purgeCredentials(ctx context.Context, d port.TenantPurgeDue, version int64) (int64, error) {
	if !d.Credentials {
		return version, nil
	}
	if p.deps.Secrets == nil {
		return version, nil
	}
	for _, path := range tenantCredentialPaths(d.TenantID) {
		if err := p.deps.Retention.Current(ctx, d.TenantID, version); err != nil {
			return version, fmt.Errorf("purge tenant %s: %w", d.TenantID, err)
		}
		if err := p.deps.Secrets.Delete(ctx, path); err != nil {
			return version, fmt.Errorf("purge tenant %s: delete credential %s: %w", d.TenantID, path, err)
		}
	}
	next, err := p.deps.Retention.MarkCredentialsPurged(ctx, d.TenantID, version, p.deps.Now())
	if err != nil {
		return version, fmt.Errorf("mark tenant %s credentials purged: %w", d.TenantID, err)
	}
	return next, nil
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
		if err := p.Tick(ctx); err != nil && !isStop(ctx, err) {
			p.log.Error("tenant purge sweep failed", map[string]any{"error": err.Error()})
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
