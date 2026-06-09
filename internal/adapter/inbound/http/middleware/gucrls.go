package middleware

import (
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/gincommon"
	pgdomain "github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgcommon"
	"github.com/gin-gonic/gin"
)

// InjectGUCSet reads the tenant/user identity that gincommon.ProtectedMiddlewares
// placed into the Gin context and stores it in the Go request context via
// pgcommon.WithGUCSet so that the connection pool's GUCProvider can inject the
// correct app.tenant_id / app.user_id GUCs for every PostgreSQL connection.
//
// This middleware must be applied after gincommon.ProtectedMiddlewares on any
// route group that performs database access.
func InjectGUCSet() gin.HandlerFunc {
	return func(c *gin.Context) {
		rc, ok := gincommon.RequestContext(c)
		if !ok {
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
