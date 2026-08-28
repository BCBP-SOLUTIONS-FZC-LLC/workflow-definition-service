package handler

import "github.com/gin-gonic/gin"

func RegisterRoutes(rg *gin.RouterGroup, h *Handler) {
	idem := func(fn gin.HandlerFunc) gin.HandlerFunc {
		return WithIdempotency(h.cache, h.log, h.idempotencyTTL, fn)
	}

	wf := rg.Group("/workflows")
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

	conn := rg.Group("/connectors")
	conn.GET("/registry", h.ListConnectorRegistry)
	conn.POST("/credentials", idem(h.WriteConnectorCredential))

	rg.GET("/bpmn/allowed-elements", h.AllowedBPMNElements)

	mod := rg.Group("/modules")
	mod.GET("", h.ListModules)
	mod.POST("", idem(h.CreateModule))
	mod.GET("/:id", h.GetModule)
	mod.GET("/:id/versions", h.ListModuleVersions)
	mod.GET("/:id/versions/:version_id", h.GetModuleVersion)
	mod.POST("/:id/versions", idem(h.AddModuleVersion))
	mod.POST("/:id/versions/:version_id/publish", idem(h.PublishModuleVersion))
	mod.DELETE("/:id", idem(h.ArchiveModule))

	starters := rg.Group("/starters")
	starters.GET("", h.ListStarters)
	starters.POST("", idem(h.CreateStarter))
	starters.POST("/from-workflow-version/:version_id", idem(h.CreateStarterFromWorkflowVersion))
	starters.GET("/:id", h.GetStarter)
	starters.DELETE("/:id", idem(h.DeleteStarter))
}

func RegisterInternalRoutes(rg *gin.RouterGroup, h *Handler) {
	rg.POST("/events", h.HandleInternalEvent)
	rg.GET("/connector-aliases", h.ListConnectorAliases)
	rg.POST("/connector-aliases/rest", h.WriteConnectorRestAlias)
	rg.POST("/connector-aliases/sql", h.WriteConnectorSQLAlias)
	rg.DELETE("/connector-aliases/rest/:alias", h.DeleteConnectorRestAlias)
	rg.DELETE("/connector-aliases/sql/:alias", h.DeleteConnectorSQLAlias)
}
