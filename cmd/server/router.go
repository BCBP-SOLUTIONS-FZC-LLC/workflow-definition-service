package main

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgcommon"

	inboundhttp "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http"
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
	r.MaxMultipartMemory = 10 << 20 // 10 MB, matches LimitRequestBody middleware cap

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

	if cfg.AppEnv == "dev" {
		r.StaticFile("/api/openapi.yaml", "docs/swagger/openapi.yaml")
		stdSwagger := ginSwagger.WrapHandler(swaggerFiles.Handler, ginSwagger.URL("/api/openapi.yaml"))
		r.GET("/swagger/*any", func(c *gin.Context) {
			switch {
			case strings.HasSuffix(c.Request.URL.Path, "/index.css"):
				inboundhttp.SwaggerThemeHandler(c)
			case strings.HasSuffix(c.Request.URL.Path, "/swagger-initializer.js"):
				inboundhttp.SwaggerInitializerHandler(c)
			default:
				stdSwagger(c)
			}
		})
		r.GET("/asyncapi", inboundhttp.AsyncAPIHandler)
	}

	// Internal service-to-service routes and not exposed on the public gateway.
	// handler injects the RLS GUC from the request envelope's tenant_id.
	internal := r.Group("/internal")
	internal.Use(httpmiddleware.RequireInternalToken(cfg.InternalAPIToken))
	internal.Use(httpmiddleware.InjectGUCSet(log))
	internal.POST("/events", h.HandleInternalEvent)

	api := r.Group("/api/v1")
	for _, mw := range gincommon.ProtectedMiddlewares(mwCfg) {
		api.Use(mw)
	}
	api.Use(httpmiddleware.InjectGUCSet(log))
	api.Use(httpmiddleware.LimitRequestBody())
	api.Use(httpmiddleware.RequireJSONContentType())

	idem := func(fn gin.HandlerFunc) gin.HandlerFunc {
		return handler.WithIdempotency(cache, log, cfg.IdempotencyTTL, fn)
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
