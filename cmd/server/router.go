package main

import (
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/config"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

func newRouter(cfg *config.Config, pool *pgcommon.Pool, cache port.CacheStore, log port.Logger, h *handler.Handler) *gin.Engine {
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
	r.GET("/readyz", readyzHandler(pool, cache))
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	api := r.Group("/api/v1")
	for _, mw := range gincommon.ProtectedMiddlewares(mwCfg) {
		api.Use(mw)
	}

	wf := api.Group("/workflows")
	wf.GET("", h.ListWorkflows)
	wf.POST("", h.CreateWorkflow)
	wf.POST("/validate", h.ValidateBPMN)
	wf.GET("/:id", h.GetWorkflow)
	wf.GET("/:id/versions", h.ListVersions)
	wf.GET("/:id/draft", h.GetDraft)
	wf.POST("/:id/draft", h.InitDraft)
	wf.PUT("/:id/draft", h.UpdateDraft)
	wf.DELETE("/:id/draft", h.DiscardDraft)
	wf.POST("/:id/archive", h.ArchiveWorkflow)
	wf.GET("/:id/versions/:version_id", h.GetVersion)
	wf.POST("/:id/versions/:version_id/publish", h.PublishVersion)
	wf.POST("/:id/versions/:version_id/clone", h.CloneVersion)
	wf.POST("/:id/versions/:version_id/promote", h.PromoteVersion)
	wf.GET("/:id/versions/:version_id/export", h.ExportBPMN)
	wf.GET("/:id/versions/:version_id/diff/:target_version_id", h.GetVersionDiff)

	return r
}
