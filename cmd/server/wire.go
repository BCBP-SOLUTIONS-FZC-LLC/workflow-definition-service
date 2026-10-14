package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/logger"

	inboundhttp "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http"
	httphandler "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	outboundhttp "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/http"
	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	bpmncompiler "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/config"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
)

func newApp(cfg *config.Config) (*app, error) {
	log, err := logger.NewLogger(cfg.AppEnv)
	if err != nil {
		return nil, fmt.Errorf("logger: %w", err)
	}

	ginCfg := ginConfig(cfg, log)
	tracingShutdown, err := initTelemetry(ginCfg, log)
	if err != nil {
		return nil, err
	}

	pools, poolWarnings := newPoolConfigs(cfg, log)
	for _, w := range poolWarnings {
		log.Warn("postgres config warning", map[string]any{"key": w.Key, "reason": w.Reason})
	}

	pool, err := newDBPool(context.Background(), pools, cfg.DatabaseFallbackURL != "")
	if err != nil {
		tracingShutdown()
		return nil, err
	}

	systemPool, err := newSystemDBPool(context.Background(), pools)
	if err != nil {
		pool.Close()
		tracingShutdown()
		return nil, err
	}

	roleCtx, cancelRoleCheck := context.WithTimeout(context.Background(), 10*time.Second)
	roleWarnings, appRole, systemRole, err := verifyPoolRoles(roleCtx, pool, systemPool, cfg.AppEnv, cfg.SystemDatabaseURL != "")
	cancelRoleCheck()
	if err != nil {
		pool.Close()
		systemPool.Close()
		tracingShutdown()
		return nil, err
	}
	for _, msg := range roleWarnings {
		log.Warn(msg, nil)
	}
	log.Info("database role posture verified", map[string]any{
		"app_role": appRole.Role, "system_role": systemRole.Role, "system_bypassrls": systemRole.BypassesRLS(),
	})

	rlsExporter := service.NewRLSViolationExporter(service.RLSViolationExporterDeps{
		Log:       pgadapter.NewRLSViolationRepo(systemPool),
		Record:    observability.AddRLSViolations,
		Interval:  cfg.RLSViolationExportInterval,
		Retention: cfg.RLSViolationRetention,
		Logger:    log,
	})

	cache, redisClient := newCacheStore(context.Background(), cfg, log)

	membershipSvc := newMembershipService(cfg)
	executionSvc := newExecutionService(cfg)
	var secretsSvc port.SecretsClient

	if cfg.OpenBaoAddr != "" {
		secretsSvc = outboundhttp.NewSecretsClient(cfg.OpenBaoAddr, cfg.OpenBaoToken, cfg.OpenBaoMount, cfg.SecretsClientTimeout)
	}

	transactor := pgadapter.NewTransactor(pool)
	globalTransactor := pgadapter.NewTransactor(systemPool)
	workflowRepo := pgadapter.NewWorkflowRepo(pool)
	versionRepo := pgadapter.NewWorkflowVersionRepo(pool)
	assigneeRepo := pgadapter.NewAssigneeRepo(pool)
	connectorAliasRepo := pgadapter.NewConnectorAliasRepo(pool)
	moduleRepo := pgadapter.NewModuleRepo(pool)
	moduleVersionRepo := pgadapter.NewModuleVersionRepo(pool)
	globalModuleRepo := pgadapter.NewModuleRepo(systemPool)
	globalModuleVersionRepo := pgadapter.NewModuleVersionRepo(systemPool)
	starterRepo := pgadapter.NewStarterRepo(pool)
	globalStarterRepo := pgadapter.NewStarterRepo(systemPool)
	compiler := bpmncompiler.New()

	workflowSvc := service.NewWorkflowService(service.WorkflowDeps{
		Transactor:     transactor,
		Workflows:      workflowRepo,
		Versions:       versionRepo,
		Cache:          cache,
		Execution:      executionSvc,
		Compiler:       compiler,
		ModuleVersions: moduleVersionRepo,
		Log:            log,
	})
	draftSvc := service.NewDraftService(service.DraftDeps{
		Transactor: transactor,
		Cache:      cache,
		Workflows:  workflowRepo,
		Versions:   versionRepo,
		Assignees:  assigneeRepo,
		Compiler:   compiler,
		Log:        log,
	})
	versionSvc := service.NewVersionService(service.VersionDeps{
		Transactor:     transactor,
		Workflows:      workflowRepo,
		Versions:       versionRepo,
		Assignees:      assigneeRepo,
		Membership:     membershipSvc,
		Execution:      executionSvc,
		Compiler:       compiler,
		ModuleVersions: moduleVersionRepo,
		Cache:          cache,
		Log:            log,
	})

	validationSvc := service.NewValidationService(service.ValidationDeps{
		Compiler:       compiler,
		ModuleVersions: moduleVersionRepo,
		Log:            log,
	})

	connectorSvc := service.NewConnectorService(service.ConnectorDeps{
		Secrets:           secretsSvc,
		Aliases:           connectorAliasRepo,
		Log:               log,
		AliasAllowedHosts: cfg.ConnectorAliasAllowedHosts,
	})

	moduleSvc := service.NewModuleService(service.ModuleDeps{
		Transactor:       transactor,
		GlobalTransactor: globalTransactor,
		Modules:          moduleRepo,
		GlobalModules:    globalModuleRepo,
		Versions:         moduleVersionRepo,
		GlobalVersions:   globalModuleVersionRepo,
		WorkflowVersions: versionRepo,
		Compiler:         compiler,
		Log:              log,
	})

	starterSvc := service.NewStarterService(service.StarterDeps{
		Starters:             starterRepo,
		GlobalStarters:       globalStarterRepo,
		Versions:             versionRepo,
		Compiler:             compiler,
		ModuleVersions:       moduleVersionRepo,
		GlobalModuleVersions: globalModuleVersionRepo,
		Log:                  log,
	})

	h := httphandler.New(httphandler.Services{
		Workflows:  workflowSvc,
		Drafts:     draftSvc,
		Versions:   versionSvc,
		Validation: validationSvc,
		Modules:    moduleSvc,
		Starters:   starterSvc,
		Membership: versionSvc,
		Connectors: connectorSvc,
		CompiledPlans: service.NewCompiledPlanService(service.CompiledPlanDeps{
			Versions: versionRepo, Cache: cache, CacheTTL: cfg.CacheCompiledPlanTTL, Log: log,
		}),
		Cache:          cache,
		IdempotencyTTL: cfg.IdempotencyTTL,
		Log:            log,
	})

	router := inboundhttp.NewRouter(inboundhttp.RouterConfig{
		GinConfig:        ginCfg,
		AppEnv:           cfg.AppEnv,
		InternalAPIToken: cfg.InternalAPIToken,
		Log:              log,
		Handler:          h,
		DB:               dbPinger{pool: pool},
		Cache:            cache,
	})

	srv := newHTTPServer(cfg.HTTPPort, router.Handler())

	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", gincommon.MetricsHandler())
	metricsSrv := newMetricsServer(cfg.MetricsPort, metricsMux)

	return &app{
		cfg:           cfg,
		log:           log,
		pool:          pool,
		systemPool:    systemPool,
		cache:         cache,
		httpServer:    srv,
		metricsServer: metricsSrv,
		rlsExporter:   rlsExporter,
		shutdown:      tracingShutdown,
		beginDrain:    router.BeginDrain,
		cacheClose:    redisClient,
	}, nil
}
