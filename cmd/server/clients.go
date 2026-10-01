package main

import (
	outboundhttp "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/http"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/config"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

// The optional outbound clients are nil interfaces when unset, never a nil
// pointer inside one, so a service's nil check skips them.

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
