package main

import (
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/events"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgmetrics"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/config"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
)

func ginConfig(cfg *config.Config, log port.Logger) gincommon.Config {
	return gincommon.Config{
		Logger:       log,
		ServiceName:  cfg.OTELServiceName,
		BuildVersion: version,
		Domain:       observability.Domain,
		Environment:  cfg.AppEnv,
	}
}

func initTelemetry(ginCfg gincommon.Config, log port.Logger) (func(), error) {
	shutdown, err := gincommon.InitTracingWithConfig(gincommon.TracingConfig{
		ServiceName:  ginCfg.ServiceName,
		BuildVersion: ginCfg.BuildVersion,
		Domain:       ginCfg.Domain,
		Environment:  ginCfg.Environment,
		Logger:       ginCfg.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("tracing: %w", err)
	}

	_ = gincommon.ObservabilityMiddlewares(ginCfg)
	observability.Register()

	identity := gincommon.MetricsConstLabels()
	registerer := gincommon.MetricsRegisterer()

	eventWarnings, err := events.InitMetrics(events.MetricsIdentityFromLabels(identity), registerer)
	if err != nil {
		shutdown()
		return nil, fmt.Errorf("platform-events metrics: %w", err)
	}
	for _, w := range eventWarnings {
		log.Warn("platform-events metric not registered", map[string]any{"metric": w.Metric, "error": w.Error()})
	}

	pgWarnings, err := pgmetrics.InitWithIdentity(pgmetrics.IdentityFromLabels(identity), registerer)
	if err != nil {
		shutdown()
		return nil, fmt.Errorf("platform-pgcommon metrics: %w", err)
	}
	for _, w := range pgWarnings {
		log.Warn("platform-pgcommon metric not registered", map[string]any{"metric": w.Metric, "error": w.Error()})
	}

	return shutdown, nil
}
