package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/logger"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgmetrics"

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

	ginCfg := gincommon.Config{
		Logger:       log,
		ServiceName:  cfg.OTELServiceName,
		BuildVersion: version,
		Domain:       observability.Domain,
		Environment:  cfg.AppEnv,
	}
	tracingShutdown, err := gincommon.InitTracingWithConfig(gincommon.TracingConfig{
		ServiceName:  ginCfg.ServiceName,
		BuildVersion: ginCfg.BuildVersion,
		Domain:       ginCfg.Domain,
		Environment:  ginCfg.Environment,
		Logger:       log,
	})
	if err != nil {
		return nil, fmt.Errorf("tracing: %w", err)
	}

	_ = gincommon.ObservabilityMiddlewares(ginCfg)
	observability.Register()
	pgWarnings, err := pgmetrics.InitWithIdentity(pgmetrics.IdentityFromLabels(gincommon.MetricsConstLabels()), gincommon.MetricsRegisterer())
	if err != nil {
		tracingShutdown()
		return nil, fmt.Errorf("pgcommon metrics: %w", err)
	}
	for _, w := range pgWarnings {
		log.Warn("platform-pgcommon metric not registered", map[string]any{"metric": w.Metric, "error": w.Error()})
	}

	pool, err := newDBPool(context.Background(), cfg, log)
	if err != nil {
		tracingShutdown()
		return nil, err
	}

	systemPool, err := newSystemDBPool(context.Background(), cfg, log)
	if err != nil {
		pool.Close()
		tracingShutdown()
		return nil, err
	}

	cache, redisClient, err := newCacheStore(context.Background(), cfg)
	if err != nil {
		pool.Close()
		systemPool.Close()
		tracingShutdown()
		return nil, err
	}

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

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.HTTPPort),
		Handler:      router.Handler(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", gincommon.MetricsHandler())
	metricsSrv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.MetricsPort),
		Handler: metricsMux,
	}

	return &app{
		cfg:           cfg,
		log:           log,
		pool:          pool,
		systemPool:    systemPool,
		cache:         cache,
		httpServer:    srv,
		metricsServer: metricsSrv,
		shutdown:      tracingShutdown,
		cacheClose:    redisClient,
	}, nil
}
