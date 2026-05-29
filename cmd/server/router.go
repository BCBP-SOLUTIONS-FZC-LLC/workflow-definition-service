package main

import (
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/config"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

func newRouter(cfg *config.Config, pool *pgxpool.Pool, log port.Logger) *gin.Engine {
	if cfg.AppEnv != "dev" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.MaxMultipartMemory = 5 << 20

	mwCfg := gincommon.Config{
		Logger:       log,
		ServiceName:  cfg.OTELServiceName,
		BuildVersion: version,
	}

	for _, mw := range gincommon.ObservabilityMiddlewares(mwCfg) {
		r.Use(mw)
	}

	r.GET("/healthz", healthzHandler)
	r.GET("/readyz", readyzHandler(pool))
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	api := r.Group("/api/v1")
	for _, mw := range gincommon.ProtectedMiddlewares(mwCfg) {
		api.Use(mw)
	}
	_ = api // TODO: register handlers

	return r
}
