package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/pkg/events"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/pkg/outbox"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/logger"
	"github.com/prometheus/client_golang/prometheus/promhttp"

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

	tracingShutdown := gincommon.InitTracingFromEnv()
	events.Init(cfg.OTELServiceName, cfg.BuildVersion)

	pool, err := newDBPool(context.Background(), cfg, log)
	if err != nil {
		tracingShutdown()
		return nil, err
	}
	observability.Register(cfg.OTELServiceName, cfg.BuildVersion, pool)

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
	closeCache := func() { _ = redisClient.Close() }

	glueCodec, err := newGlueCodec(context.Background(), cfg)
	if err != nil {
		pool.Close()
		systemPool.Close()
		closeCache()
		tracingShutdown()
		return nil, err
	}

	publisher, err := newPublisher(cfg, log, glueCodec)
	if err != nil {
		pool.Close()
		systemPool.Close()
		closeCache()
		tracingShutdown()
		return nil, err
	}

	membershipSvc := newMembershipService(cfg)
	executionSvc := newExecutionService(cfg)
	var secretsSvc port.SecretsClient

	if cfg.OpenBaoAddr != "" {
		secretsSvc = outboundhttp.NewSecretsClient(cfg.OpenBaoAddr, cfg.OpenBaoToken, cfg.OpenBaoMount, cfg.SecretsClientTimeout)
	}

	relay, err := outbox.NewRunner(outbox.Config{
		Pool:         pool,
		Publisher:    publisher,
		Logger:       log,
		PollInterval: cfg.OutboxPollInterval,
		BatchSize:    cfg.OutboxBatchSize,
	})
	if err != nil {
		pool.Close()
		systemPool.Close()
		closeCache()
		tracingShutdown()
		return nil, fmt.Errorf("outbox runner: %w", err)
	}

	transactor := pgadapter.NewTransactor(pool)
	globalTransactor := pgadapter.NewTransactor(systemPool)
	workflowRepo := pgadapter.NewWorkflowRepo(pool)
	versionRepo := pgadapter.NewWorkflowVersionRepo(pool)
	assigneeRepo := pgadapter.NewAssigneeRepo(pool)
	outboxRepo := pgadapter.NewOutboxRepo(pool)
	connectorAliasRepo := pgadapter.NewConnectorAliasRepo(pool)
	moduleRepo := pgadapter.NewModuleRepo(pool)
	moduleVersionRepo := pgadapter.NewModuleVersionRepo(pool)
	globalModuleRepo := pgadapter.NewModuleRepo(systemPool)
	globalModuleVersionRepo := pgadapter.NewModuleVersionRepo(systemPool)
	starterRepo := pgadapter.NewStarterRepo(pool)
	globalStarterRepo := pgadapter.NewStarterRepo(systemPool)
	compiler := bpmncompiler.New()

	workflowSvc := service.NewWorkflowService(service.WorkflowDeps{
		Transactor: transactor,
		Workflows:  workflowRepo,
		Versions:   versionRepo,
		Cache:      cache,
		Execution:  executionSvc,
		Compiler:   compiler,
		// ModuleVersions resolves the library modules a new workflow calls.
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
		Transactor: transactor,
		Workflows:  workflowRepo,
		Versions:   versionRepo,
		Assignees:  assigneeRepo,
		Outbox:     outboxRepo,
		Membership: membershipSvc,
		Execution:  executionSvc,
		Compiler:   compiler,
		// The RLS pool: a tenant's calls resolve its own modules and the global ones.
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
		Starters:       starterRepo,
		GlobalStarters: globalStarterRepo,
		Versions:       versionRepo,
		Compiler:       compiler,
		// A tenant starter resolves the modules it calls through RLS, a
		// global one through the system pool.
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
		// Inbound events arrive over HTTP via POST /internal/events
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
		GinConfig:        gincommon.Config{Logger: log, ServiceName: cfg.OTELServiceName, BuildVersion: version},
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
	metricsMux.Handle("/metrics", promhttp.Handler())
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
		outboxRelay:   relay,
		shutdown:      tracingShutdown,
		cacheClose:    redisClient,
	}, nil
}
