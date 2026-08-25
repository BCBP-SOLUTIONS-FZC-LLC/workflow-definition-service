package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

// restAliasResp/sqlAliasResp/ListConnectorAliases' response shape matches
// workflow-connectors/pkg/connectors/aliasconfig's Config/Endpoint/Query
// JSON encoding field-for-field (Timeout as raw nanoseconds, no tag) so
// execution_service's cmd/connector-worker can json.Unmarshal this response
// directly into aliasconfig.Config with no conversion glue.
type restAliasResp struct {
	Alias        string        `json:"alias"`
	Method       string        `json:"method"`
	BaseURL      string        `json:"baseURL"`
	PathTemplate string        `json:"pathTemplate"`
	Timeout      time.Duration `json:"timeout"`
}

type sqlAliasResp struct {
	Alias      string        `json:"alias"`
	BaseURL    string        `json:"baseURL"`
	Path       string        `json:"path"`
	QueryID    string        `json:"queryId"`
	ParamCount int           `json:"paramCount"`
	Timeout    time.Duration `json:"timeout"`
}

func (h *Handler) ListConnectorAliases(c *gin.Context) {
	rest, sql, err := h.connectors.ListAliases(c.Request.Context())
	if err != nil {
		errResponse(c, h.log, err)
		return
	}

	restResp := make([]restAliasResp, len(rest))
	for i, a := range rest {
		restResp[i] = restAliasResp{
			Alias: a.Alias, Method: a.Method, BaseURL: a.BaseURL,
			PathTemplate: a.PathTemplate, Timeout: a.Timeout,
		}
	}
	sqlResp := make([]sqlAliasResp, len(sql))
	for i, q := range sql {
		sqlResp[i] = sqlAliasResp{
			Alias: q.Alias, BaseURL: q.BaseURL, Path: q.Path,
			QueryID: q.QueryID, ParamCount: q.ParamCount, Timeout: q.Timeout,
		}
	}
	c.JSON(http.StatusOK, gin.H{"version": 1, "restCall": restResp, "sqlQuery": sqlResp})
}

type writeRestAliasReq struct {
	Alias        string `json:"alias" binding:"required"`
	Method       string `json:"method" binding:"required"`
	BaseURL      string `json:"baseURL" binding:"required"`
	PathTemplate string `json:"pathTemplate" binding:"required"`
	TimeoutMs    int64  `json:"timeoutMs"`
}

func (h *Handler) WriteConnectorRestAlias(c *gin.Context) {
	var req writeRestAliasReq
	if err := c.ShouldBindJSON(&req); err != nil {
		bindErrResponse(c, err)
		return
	}

	err := h.connectors.WriteRestAlias(c.Request.Context(), domain.ConnectorRestAlias{
		Alias:        req.Alias,
		Method:       req.Method,
		BaseURL:      req.BaseURL,
		PathTemplate: req.PathTemplate,
		Timeout:      millisOrDefault(req.TimeoutMs),
	})
	if err != nil {
		errResponse(c, h.log, err)
		return
	}
	c.Status(http.StatusNoContent)
}

type writeSQLAliasReq struct {
	Alias      string `json:"alias" binding:"required"`
	BaseURL    string `json:"baseURL" binding:"required"`
	Path       string `json:"path" binding:"required"`
	QueryID    string `json:"queryId" binding:"required"`
	ParamCount int    `json:"paramCount"`
	TimeoutMs  int64  `json:"timeoutMs"`
}

func (h *Handler) WriteConnectorSQLAlias(c *gin.Context) {
	var req writeSQLAliasReq
	if err := c.ShouldBindJSON(&req); err != nil {
		bindErrResponse(c, err)
		return
	}

	err := h.connectors.WriteSQLAlias(c.Request.Context(), domain.ConnectorSQLAlias{
		Alias:      req.Alias,
		BaseURL:    req.BaseURL,
		Path:       req.Path,
		QueryID:    req.QueryID,
		ParamCount: req.ParamCount,
		Timeout:    millisOrDefault(req.TimeoutMs),
	})
	if err != nil {
		errResponse(c, h.log, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) DeleteConnectorRestAlias(c *gin.Context) {
	deleted, err := h.connectors.DeleteRestAlias(c.Request.Context(), c.Param("alias"))
	if err != nil {
		errResponse(c, h.log, err)
		return
	}
	if !deleted {
		writeProblem(c, http.StatusNotFound, CodeNotFound, "alias not found", nil)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) DeleteConnectorSQLAlias(c *gin.Context) {
	deleted, err := h.connectors.DeleteSQLAlias(c.Request.Context(), c.Param("alias"))
	if err != nil {
		errResponse(c, h.log, err)
		return
	}
	if !deleted {
		writeProblem(c, http.StatusNotFound, CodeNotFound, "alias not found", nil)
		return
	}
	c.Status(http.StatusNoContent)
}

func millisOrDefault(ms int64) time.Duration {
	if ms <= 0 {
		return 5 * time.Second
	}
	return time.Duration(ms) * time.Millisecond
}
