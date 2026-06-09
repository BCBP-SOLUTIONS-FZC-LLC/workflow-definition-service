package main

import (
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	httpmiddleware "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/middleware"
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
	api.Use(httpmiddleware.InjectGUCSet(log))
	api.Use(httpmiddleware.LimitRequestBody())

	idem := func(fn gin.HandlerFunc) gin.HandlerFunc {
		return handler.WithIdempotency(cache, log, fn)
	}

	wf := api.Group("/workflows")
	wf.GET("", h.ListWorkflows)
	wf.POST("", idem(h.CreateWorkflow))
	wf.POST("/validate", h.ValidateBPMN)
	wf.GET("/:id", h.GetWorkflow)
	wf.POST("/:id/archive", idem(h.ArchiveWorkflow))

	draft := wf.Group("/:id/draft")
	draft.GET("", h.GetDraft)
	draft.POST("", idem(h.InitDraft))
	draft.PUT("", idem(h.UpdateDraft))
	draft.DELETE("", idem(h.DiscardDraft))

	ver := wf.Group("/:id/versions")
	ver.GET("", h.ListVersions)
	ver.GET("/:version_id", h.GetVersion)
	ver.POST("/:version_id/publish", idem(h.PublishVersion))
	ver.POST("/:version_id/clone", idem(h.CloneVersion))
	ver.POST("/:version_id/promote", idem(h.PromoteVersion))
	ver.GET("/:version_id/export", h.ExportBPMN)
	ver.GET("/:version_id/diff/:target_version_id", h.GetVersionDiff)

	return r
}
