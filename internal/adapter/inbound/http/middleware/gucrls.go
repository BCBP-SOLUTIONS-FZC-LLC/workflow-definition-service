package middleware

import (
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/gincommon"
	pgdomain "github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/gin-gonic/gin"
)

func InjectGUCSet(log port.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		rc, ok := gincommon.RequestContext(c)
		if !ok {
			if log != nil {
				log.Warn("InjectGUCSet: RequestContext missing, GUC not set, RLS unenforced; check ProtectedMiddlewares wiring", map[string]any{
					"path":   c.FullPath(),
					"method": c.Request.Method,
				})
			}
			c.Next()
			return
		}
		enriched := pgcommon.WithGUCSet(c.Request.Context(), pgdomain.GUCSet{
			TenantID:    rc.TenantID,
			UserID:      rc.UserID,
			TenantRoles: rc.Roles,
		})
		c.Request = c.Request.WithContext(enriched)
		c.Next()
	}
}
