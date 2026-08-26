package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/pkg/events"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/pkg/outbox"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/grpccommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/logger"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgmetrics"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	definitionv1 "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/gen/proto/definition/v1"
	grpcadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/grpc"
	httphandler "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	outboundgrpc "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/grpc"
	outboundhttp "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/http"
	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	bpmncompiler "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/config"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func newApp(cfg *config.Config) (*app, error) {
	log, err := logger.NewLogger(cfg.AppEnv)
	if err != nil {
		return nil, fmt.Errorf("logger: %w", err)
	}

	tracingShutdown := gincommon.InitTracingFromEnv()
	events.Init(cfg.OTELServiceName, cfg.BuildVersion)
	pgmetrics.Init(cfg.OTELServiceName, cfg.BuildVersion)

	pool, err := newDBPool(context.Background(), cfg)
	if err != nil {
		tracingShutdown()
		return nil, err
	}
	prometheus.MustRegister(pgmetrics.NewPoolStatsCollector(pool, cfg.OTELServiceName))

	systemPool, err := newSystemDBPool(context.Background(), cfg)
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

	var membershipSvc *outboundhttp.MembershipClient
	var executionSvc *outboundgrpc.ExecutionClient
	var secretsSvc port.SecretsClient

	if cfg.OrgMembershipBaseURL != "" {
		membershipSvc = outboundhttp.NewMembershipClient(cfg.OrgMembershipBaseURL, cfg.MembershipClientTimeout)
	}
	if cfg.ExecutionServiceAddr != "" {
		executionSvc, err = outboundgrpc.NewExecutionClient(cfg.ExecutionServiceAddr, cfg.ExecutionClientTimeout)
		if err != nil {
			pool.Close()
			systemPool.Close()
			closeCache()
			tracingShutdown()
			return nil, fmt.Errorf("execution client: %w", err)
		}
	}
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
	processedEventRepo := pgadapter.NewProcessedEventRepo(pool)
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
		Log:        log,
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
		Transactor:      transactor,
		Workflows:       workflowRepo,
		Versions:        versionRepo,
		Assignees:       assigneeRepo,
		Outbox:          outboxRepo,
		ProcessedEvents: processedEventRepo,
		Membership:      membershipSvc,
		Execution:       executionSvc,
		Compiler:        compiler,
		Cache:           cache,
		Log:             log,
	})

	validationSvc := service.NewValidationService(service.ValidationDeps{
		Compiler: compiler,
		Log:      log,
	})

	connectorSvc := service.NewConnectorService(service.ConnectorDeps{
		Secrets: secretsSvc,
		Aliases: connectorAliasRepo,
		Log:     log,
	})

	moduleSvc := service.NewModuleService(service.ModuleDeps{
		Transactor:       transactor,
		GlobalTransactor: globalTransactor,
		Modules:          moduleRepo,
		GlobalModules:    globalModuleRepo,
		Versions:         moduleVersionRepo,
		GlobalVersions:   globalModuleVersionRepo,
		Compiler:         compiler,
		Log:              log,
	})

	starterSvc := service.NewStarterService(service.StarterDeps{
		Starters:       starterRepo,
		GlobalStarters: globalStarterRepo,
		Versions:       versionRepo,
		Compiler:       compiler,
		Log:            log,
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
		Log:        log,
	})

	grpcCfg := grpccommon.Config{
		ServiceName:  cfg.OTELServiceName,
		BuildVersion: cfg.BuildVersion,
	}
	grpcSrv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(grpccommon.DefaultUnaryInterceptors(grpcCfg)...),
		grpc.ChainStreamInterceptor(grpccommon.DefaultStreamInterceptors(grpcCfg)...),
	)
	definitionv1.RegisterDefinitionServiceServer(grpcSrv, grpcadapter.NewServer(log, versionRepo, cache, cfg.CacheCompiledPlanTTL))
	grpc_health_v1.RegisterHealthServer(grpcSrv, health.NewServer())
	reflection.Register(grpcSrv)

	r := newRouter(cfg, pool, cache, log, h)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.HTTPPort),
		Handler:      r,
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
		cfg:            cfg,
		log:            log,
		pool:           pool,
		systemPool:     systemPool,
		cache:          cache,
		httpServer:     srv,
		metricsServer:  metricsSrv,
		grpcServer:     grpcSrv,
		outboxRelay:    relay,
		shutdown:       tracingShutdown,
		cacheClose:     redisClient,
		executionClose: executionSvc,
	}, nil
}
