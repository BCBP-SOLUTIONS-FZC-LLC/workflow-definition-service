package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
)

const systemCallerRole = "iam-system"

// RequireSystemCaller admits a request only when x-tenant-roles lists the
// system role and x-user-id is set. The headers are not a credential: the
// mesh authenticates the calling workload (definition LLD 3.4).
func RequireSystemCaller() gin.HandlerFunc {
	return func(c *gin.Context) {
		reason := ""
		switch {
		case strings.TrimSpace(c.GetHeader("x-user-id")) == "":
			reason = "missing_user"
		case !hasSystemRole(c.GetHeader("x-tenant-roles")):
			reason = "missing_role"
		}
		if reason != "" {
			observability.IncCounterVec(observability.SystemCallerRejectedTotal, reason)
			abortProblem(c, http.StatusUnauthorized, "https://api.workflow.platform/errors/unauthorized",
				"Unauthorized", "UNAUTHORIZED", "missing or invalid internal caller identity")
			return
		}
		c.Next()
	}
}

func hasSystemRole(roles string) bool {
	for _, r := range strings.Split(roles, ",") {
		if strings.TrimSpace(r) == systemCallerRole {
			return true
		}
	}
	return false
}
