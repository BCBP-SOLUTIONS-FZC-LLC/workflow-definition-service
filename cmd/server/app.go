package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/grpccommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/logger"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgcommon"

	definitionv1 "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/gen/proto/definition/v1"
	grpcadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/grpc"
	httphandler "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/sqs"
	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	snspub "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/sns"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/valkey"
	bpmncompiler "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/config"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/outbox"
)

type app struct {
	cfg         *config.Config
	log         port.Logger
	pool        *pgcommon.Pool
	cache       port.CacheStore
	httpServer  *http.Server
	grpcServer  *grpc.Server
	sqsConsumer *sqs.StubConsumer
	outboxRelay *outbox.Relay
	shutdown    func()
}

func newApp(cfg *config.Config) (*app, error) {
	log, err := logger.NewLogger(cfg.AppEnv)
	if err != nil {
		return nil, fmt.Errorf("logger: %w", err)
	}

	tracingShutdown := gincommon.InitTracingFromEnv()

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

	publisher := snspub.NewStubPublisher(log)
	sqsConsumer := sqs.NewStubConsumer(log)

	outboxRepo := pgadapter.NewOutboxRepo(pool)
	relay := outbox.NewRelay(outboxRepo, publisher, cfg.OutboxPollInterval, cfg.OutboxBatchSize, log)

	workflowRepo := pgadapter.NewWorkflowRepo(pool)
	versionRepo := pgadapter.NewWorkflowVersionRepo(pool)
	assigneeRepo := pgadapter.NewAssigneeRepo(pool)
	compiler := bpmncompiler.New()

	workflowSvc := service.NewWorkflowService(service.WorkflowDeps{
		Workflows: workflowRepo,
		Versions:  versionRepo,
		Outbox:    outboxRepo,
		Cache:     cache,
		Log:       log,
	})
	draftSvc := service.NewDraftService(service.DraftDeps{
		Workflows: workflowRepo,
		Versions:  versionRepo,
		Assignees: assigneeRepo,
		Compiler:  compiler,
		Log:       log,
	})
	versionSvc := service.NewVersionService(service.VersionDeps{
		Workflows: workflowRepo,
		Versions:  versionRepo,
		Assignees: assigneeRepo,
		Outbox:    outboxRepo,
		Compiler:  compiler,
		Log:       log,
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

	r := newRouter(cfg, pool, log, h)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.HTTPPort),
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return &app{
		cfg:         cfg,
		log:         log,
		pool:        pool,
		cache:       cache,
		httpServer:  srv,
		grpcServer:  grpcSrv,
		sqsConsumer: sqsConsumer,
		outboxRelay: relay,
		shutdown:    tracingShutdown,
	}, nil
}

func newDBPool(ctx context.Context, cfg *config.Config) (*pgcommon.Pool, error) {
	pool, err := pgcommon.NewPool(ctx, pgcommon.Config{
		DSN:                cfg.DatabaseURL,
		MaxConns:           cfg.PGMaxConns,
		MinConns:           cfg.PGMinConns,
		SlowQueryThreshold: time.Duration(cfg.PGSlowQueryThresholdMS) * time.Millisecond,
		GUCProvider:        pgcommon.GUCSetFromContext,
	})
	if err != nil {
		return nil, fmt.Errorf("db pool: %w", err)
	}
	return pool, nil
}

func newCacheStore(ctx context.Context, cfg *config.Config) (port.CacheStore, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.ValkeyAddr,
		Password: cfg.ValkeyPassword,
	})
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("valkey ping: %w", err)
	}
	return valkey.NewCache(client), nil
}

func (a *app) run() {
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		a.log.Info("HTTP server starting", map[string]any{"addr": a.httpServer.Addr})
		if err := a.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			a.log.Fatal("HTTP server error", map[string]any{"error": err.Error()})
		}
	}()

	go func() {
		addr := fmt.Sprintf(":%d", a.cfg.GRPCPort)
		lis, err := net.Listen("tcp", addr)
		if err != nil {
			a.log.Fatal("gRPC listen error", map[string]any{"error": err.Error()})
			return
		}
		a.log.Info("gRPC server starting", map[string]any{"addr": addr})
		if err := a.grpcServer.Serve(lis); err != nil {
			a.log.Error("gRPC server exited", map[string]any{"error": err.Error()})
		}
	}()

	go func() {
		if err := a.sqsConsumer.Run(ctx); err != nil {
			a.log.Error("SQS consumer exited", map[string]any{"error": err.Error()})
		}
	}()

	go func() {
		if err := a.outboxRelay.Run(ctx); err != nil {
			a.log.Error("outbox relay exited", map[string]any{"error": err.Error()})
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit

	a.log.Info("shutting down", map[string]any{"signal": sig.String()})
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := a.httpServer.Shutdown(shutdownCtx); err != nil {
		a.log.Error("HTTP server shutdown error", map[string]any{"error": err.Error()})
	}
	a.grpcServer.GracefulStop()

	a.pool.Close()
	a.shutdown()

	a.log.Info("server stopped", nil)
}
