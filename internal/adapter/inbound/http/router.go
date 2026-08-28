// Package http's router.go is the single source of truth for this
// service's HTTP surface: every path, method, middleware, and route group.
// It mirrors execution_service's own router.go — cmd/server's job is to
// construct dependencies and call NewRouter, not to encode routing
// decisions itself.
package http

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/gincommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	httpmiddleware "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/middleware"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

// Pinger is satisfied by any /readyz dependency that only needs a
// healthy/unhealthy verdict. The composition root (cmd/server) supplies one
// per dependency, so this package never imports platform-pgcommon directly.
type Pinger interface {
	Ping(ctx context.Context) error
}

// DBHealth is a decoupled copy of pgcommon.HealthStatus's fields /readyz
// actually reports.
type DBHealth struct {
	Healthy       bool
	Utilization   float64
	AcquiredConns int32
	MaxConns      int32
}

// DBPinger is Pinger's richer counterpart for the one /readyz dependency
// (Postgres) whose response carries diagnostic numbers beyond a bare
// healthy/unhealthy bit.
type DBPinger interface {
	Health(ctx context.Context) DBHealth
}

// RouterConfig bundles every dependency NewRouter needs.
type RouterConfig struct {
	GinConfig        gincommon.Config
	AppEnv           string
	InternalAPIToken string
	Log              port.Logger

	Handler *handler.Handler

	DB    DBPinger
	Cache Pinger
}

// Router owns the Gin engine for this service.
type Router struct {
	engine *gin.Engine
}

// Handler returns the http.Handler to serve.
func (r *Router) Handler() http.Handler { return r.engine }

// NewRouter builds and wires every route this service exposes: the dev-only
// swagger/AsyncAPI doc routes, the unauthenticated infra probes, and the
// protected /internal + /api/v1 route groups.
func NewRouter(cfg RouterConfig) *Router {
	if cfg.AppEnv != "dev" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.MaxMultipartMemory = 10 << 20 // 10 MB, matches LimitRequestBody middleware cap

	for _, mw := range gincommon.ObservabilityMiddlewares(cfg.GinConfig) {
		r.Use(mw)
	}

	r.GET("/healthz", gincommon.HealthHandler())
	r.GET("/readyz", readyzHandler(cfg.DB, cfg.Cache))

	if cfg.AppEnv == "dev" {
		r.StaticFile("/api/openapi.yaml", "docs/swagger/openapi.yaml")
		stdSwagger := ginSwagger.WrapHandler(swaggerFiles.Handler, ginSwagger.URL("/api/openapi.yaml"))
		r.GET("/swagger/*any", func(c *gin.Context) {
			switch {
			case strings.HasSuffix(c.Request.URL.Path, "/index.css"):
				SwaggerThemeHandler(c)
			case strings.HasSuffix(c.Request.URL.Path, "/swagger-initializer.js"):
				SwaggerInitializerHandler(c)
			default:
				stdSwagger(c)
			}
		})
		r.GET("/asyncapi", AsyncAPIHandler)
	}

	// Internal service-to-service routes, not exposed on the public gateway.
	// handler injects the RLS GUC from the request envelope's tenant_id.
	internal := r.Group("/internal")
	internal.Use(httpmiddleware.RequireInternalToken(cfg.InternalAPIToken))
	internal.Use(httpmiddleware.InjectGUCSet(cfg.Log))
	handler.RegisterInternalRoutes(internal, cfg.Handler)

	api := r.Group("/api/v1")
	for _, mw := range gincommon.ProtectedMiddlewares(cfg.GinConfig) {
		api.Use(mw)
	}
	api.Use(httpmiddleware.InjectGUCSet(cfg.Log))
	api.Use(httpmiddleware.LimitRequestBody())
	api.Use(httpmiddleware.RequireJSONContentType())
	handler.RegisterRoutes(api, cfg.Handler)

	return &Router{engine: r}
}

func readyzHandler(db DBPinger, cache Pinger) gin.HandlerFunc {
	return func(c *gin.Context) {
		hs := db.Health(c.Request.Context())
		if !hs.Healthy {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable", "db": "unreachable"})
			return
		}
		if err := cache.Ping(c.Request.Context()); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable", "cache": "unreachable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"status":         "OK",
			"db_utilization": hs.Utilization,
			"db_conns":       hs.AcquiredConns,
			"db_max_conns":   hs.MaxConns,
		})
	}
}
