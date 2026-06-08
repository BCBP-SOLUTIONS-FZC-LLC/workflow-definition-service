package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"google.golang.org/grpc"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/pkg/events"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/pkg/outbox"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/grpccommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/logger"

	definitionv1 "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/gen/proto/definition/v1"
	grpcadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/grpc"
	httphandler "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	outboundgrpc "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/grpc"
	outboundhttp "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/http"
	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	bpmncompiler "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/config"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func newApp(cfg *config.Config) (*app, error) {
	log, err := logger.NewLogger(cfg.AppEnv)
	if err != nil {
		return nil, fmt.Errorf("logger: %w", err)
	}

	tracingShutdown := gincommon.InitTracingFromEnv()
	events.Init(cfg.OTELServiceName, cfg.BuildVersion)

	pool, err := newDBPool(context.Background(), cfg)
	if err != nil {
		tracingShutdown()
		return nil, err
	}

	cache, err := newCacheStore(context.Background(), cfg)
	if err != nil {
		pool.Close()
		tracingShutdown()
		return nil, err
	}

	publisher, err := newPublisher(cfg, log)
	if err != nil {
		pool.Close()
		tracingShutdown()
		return nil, err
	}

	var membershipSvc *outboundhttp.MembershipClient
	var executionSvc *outboundgrpc.ExecutionClient

	if cfg.OrgMembershipBaseURL != "" {
		membershipSvc = outboundhttp.NewMembershipClient(cfg.OrgMembershipBaseURL)
	}
	if cfg.ExecutionServiceAddr != "" {
		executionSvc, err = outboundgrpc.NewExecutionClient(cfg.ExecutionServiceAddr)
		if err != nil {
			pool.Close()
			tracingShutdown()
			return nil, fmt.Errorf("execution client: %w", err)
		}
	}

	sqsHandler := func(ctx context.Context, env events.Envelope[json.RawMessage]) error {
		log.Info("sqs handler: received event", map[string]any{
			"event_id":   env.ID,
			"event_type": env.Type,
		})
		return nil
	}

	sqsConsumer, err := newConsumer(cfg, log, sqsHandler)
	if err != nil {
		pool.Close()
		tracingShutdown()
		return nil, err
	}

	relay := outbox.NewRunner(outbox.Config{
		Pool:         pool,
		Publisher:    publisher,
		Logger:       log,
		PollInterval: cfg.OutboxPollInterval,
		BatchSize:    cfg.OutboxBatchSize,
	})

	transactor := pgadapter.NewTransactor(pool)
	workflowRepo := pgadapter.NewWorkflowRepo(pool)
	versionRepo := pgadapter.NewWorkflowVersionRepo(pool)
	assigneeRepo := pgadapter.NewAssigneeRepo(pool)
	outboxRepo := pgadapter.NewOutboxRepo(pool)
	compiler := bpmncompiler.New()

	workflowSvc := service.NewWorkflowService(service.WorkflowDeps{
		Transactor: transactor,
		Workflows:  workflowRepo,
		Versions:   versionRepo,
		Outbox:     outboxRepo,
		Cache:      cache,
		Execution:  executionSvc,
		Log:        log,
	})
	draftSvc := service.NewDraftService(service.DraftDeps{
		Cache:     cache,
		Workflows: workflowRepo,
		Versions:  versionRepo,
		Assignees: assigneeRepo,
		Compiler:  compiler,
		Log:       log,
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
		Log:        log,
	})
	validationSvc := service.NewValidationService(service.ValidationDeps{
		Compiler: compiler,
		Log:      log,
	})

	h := httphandler.New(httphandler.Services{
		Workflows:  workflowSvc,
		Drafts:     draftSvc,
		Versions:   versionSvc,
		Validation: validationSvc,
	})

	grpcCfg := grpccommon.Config{
		ServiceName:  cfg.OTELServiceName,
		BuildVersion: cfg.BuildVersion,
	}
	grpcSrv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(grpccommon.DefaultUnaryInterceptors(grpcCfg)...),
		grpc.ChainStreamInterceptor(grpccommon.DefaultStreamInterceptors(grpcCfg)...),
	)
	definitionv1.RegisterDefinitionServiceServer(grpcSrv, grpcadapter.NewServer(log))

	r := newRouter(cfg, pool, cache, log, h)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.HTTPPort),
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return &app{
		cfg:            cfg,
		log:            log,
		pool:           pool,
		cache:          cache,
		httpServer:     srv,
		grpcServer:     grpcSrv,
		sqsConsumer:    sqsConsumer,
		outboxRelay:    relay,
		shutdown:       tracingShutdown,
		executionClose: executionSvc,
	}, nil
}
