package main

import (
	"fmt"

	outboundhttp "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/http"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/config"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func newExecutionService(cfg *config.Config) port.ExecutionService {
	if cfg.ExecutionServiceURL == "" {
		return nil
	}
	return outboundhttp.NewExecutionClient(cfg.ExecutionServiceURL, cfg.ExecutionClientTimeout)
}

func newMembershipService(cfg *config.Config) port.MembershipService {
	if cfg.OrgMembershipBaseURL == "" {
		return nil
	}
	return outboundhttp.NewMembershipClient(cfg.OrgMembershipBaseURL, cfg.MembershipClientTimeout)
}

// newPlanLimits returns a nil source when neither address is set. A setting the Entitlements cache refuses is an
// error, so a bad value stops startup instead of leaving limits unenforced.
func newPlanLimits(cfg *config.Config, log port.Logger) (port.PlanLimits, error) {
	if cfg.EntitlementsBaseURL != "" {
		fields := map[string]any{"source": "entitlements"}
		if cfg.CatalogAdminBaseURL != "" {
			fields["plan_table_ignored"] = true
		}
		log.Info("plan limit source chosen", fields)
		limits, err := service.NewEntitlementsLimitService(service.EntitlementsLimitDeps{
			Reader:         outboundhttp.NewEntitlementsClient(cfg.EntitlementsBaseURL, cfg.EntitlementsClientTimeout),
			TTL:            cfg.EntitlementsCacheTTL,
			FreshnessBound: cfg.EntitlementsFreshnessBound,
			Log:            log,
		})
		if err != nil {
			return nil, fmt.Errorf("plan limits: %w", err)
		}
		return limits, nil
	}
	if cfg.CatalogAdminBaseURL == "" {
		log.Warn("no plan limit source configured, creating, cloning and promoting workflows will answer 503", map[string]any{"source": "none"})
		return nil, nil
	}
	log.Info("plan limit source chosen", map[string]any{"source": "plan_table"})
	return service.NewPlanLimitService(service.PlanLimitDeps{
		Reader: outboundhttp.NewCatalogClient(cfg.CatalogAdminBaseURL, cfg.CatalogClientTimeout),
		TTL:    cfg.PlanTableTTL,
		Log:    log,
	}), nil
}

func newSecretsClient(cfg *config.Config) port.SecretsClient {
	if cfg.OpenBaoAddr == "" {
		return nil
	}
	return outboundhttp.NewSecretsClient(cfg.OpenBaoAddr, cfg.OpenBaoToken, cfg.OpenBaoMount, cfg.SecretsClientTimeout)
}
