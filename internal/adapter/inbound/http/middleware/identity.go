package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/gincommon"
	pgdomain "github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

const identityKey = "workflow.identity"

type Identity struct {
	TenantID uuid.UUID
	UserID   uuid.UUID
	Roles    []string
}

func IdentityFrom(c *gin.Context) (Identity, bool) {
	v, ok := c.Get(identityKey)
	if !ok {
		return Identity{}, false
	}
	id, ok := v.(Identity)
	return id, ok
}

func RequireIdentity(log port.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		rc, ok := gincommon.RequestContext(c)
		if !ok {
			if log != nil {
				log.Error("RequireIdentity: RequestContext missing; check ProtectedMiddlewares wiring", map[string]any{
					"path":   c.FullPath(),
					"method": c.Request.Method,
				})
			}
			abortIdentity(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal Server Error", "missing request context")
			return
		}
		tenantID, err := uuid.Parse(rc.TenantID)
		if err != nil {
			abortIdentity(c, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized Access", "x-tenant-id must be a UUID")
			return
		}
		userID, err := uuid.Parse(rc.UserID)
		if err != nil {
			abortIdentity(c, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized Access", "x-user-id must be a UUID")
			return
		}
		ctx, err := pgcommon.WithValidatedGUCSet(c.Request.Context(), pgdomain.GUCSet{
			TenantID:    tenantID.String(),
			UserID:      userID.String(),
			TenantRoles: rc.Roles,
		})
		if err != nil {
			abortIdentity(c, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized Access", "identity headers are not acceptable")
			return
		}
		c.Request = c.Request.WithContext(ctx)
		c.Set(identityKey, Identity{TenantID: tenantID, UserID: userID, Roles: rc.Roles})
		c.Next()
	}
}

func abortIdentity(c *gin.Context, status int, code, title, detail string) {
	typeURI := "https://api.workflow.platform/errors/unauthorized"
	if status == http.StatusInternalServerError {
		typeURI = "https://api.workflow.platform/errors/internal-error"
	}
	abortProblem(c, status, typeURI, title, code, detail)
}
