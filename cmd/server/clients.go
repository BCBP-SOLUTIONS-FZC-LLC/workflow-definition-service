package main

import (
	outboundhttp "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/http"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/config"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func newExecutionService(cfg *config.Config) port.ExecutionService {
	if cfg.ExecutionServiceURL == "" {
		return nil
	}
	return outboundhttp.NewExecutionClient(cfg.ExecutionServiceURL, cfg.ExecutionServiceToken, cfg.ExecutionClientTimeout)
}

func newMembershipService(cfg *config.Config) port.MembershipService {
	if cfg.OrgMembershipBaseURL == "" {
		return nil
	}
	return outboundhttp.NewMembershipClient(cfg.OrgMembershipBaseURL, cfg.MembershipClientTimeout)
}

func newPlanLimits(cfg *config.Config, log port.Logger) port.PlanLimits {
	if cfg.CatalogAdminBaseURL == "" {
		return nil
	}
	return service.NewPlanLimitService(service.PlanLimitDeps{
		Reader: outboundhttp.NewCatalogClient(cfg.CatalogAdminBaseURL, cfg.CatalogClientTimeout),
		TTL:    cfg.PlanTableTTL,
		Log:    log,
	})
}
