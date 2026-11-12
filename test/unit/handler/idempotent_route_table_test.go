package handler_test

import (
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	httpadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	httpmiddleware "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/middleware"
)

const idemTableKeyLimitDetail = "Idempotency-Key must be at most 256 bytes"

var idemTableParam = regexp.MustCompile(`:[a-z_]+`)

// idemTableExempt names the mutating routes the design leaves outside the
// idempotency wrapper: a stateless check and the two credential routes, whose
// secrets must never leave a cache entry behind.
var idemTableExempt = map[string]bool{
	"POST /api/v1/workflows/validate":                                   true,
	"POST /api/v1/connectors/credentials":                               true,
	"DELETE /api/v1/connectors/credentials/:connector_type/:field_name": true,
}

// idemTableEngine is the public API wired like newRouter, with a recovery layer
// because no service is wired and a handler that is reached may dereference nil.
func idemTableEngine() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.RecoveryWithWriter(io.Discard))
	for _, mw := range gincommon.ProtectedMiddlewares(gincommon.Config{}) {
		r.Use(mw)
	}
	r.Use(httpmiddleware.RequireIdentity(nil))
	registerRoutes(r, handler.New(handler.Services{Cache: newMemCache(), IdempotencyTTL: time.Hour}))
	return r
}

func idemTableProbe(method, route string) *http.Request {
	path := idemTableParam.ReplaceAllStringFunc(route, func(string) string { return uuid.NewString() })
	probe := adminReq(method, path, map[string]string{})
	probe.Header.Set("Idempotency-Key", strings.Repeat("k", 257))
	return probe
}

func TestRegisterAPIRoutes_EveryMutatingRouteExceptTheThreeExemptOnesIsIdempotent(t *testing.T) {
	r := idemTableEngine()

	var wrapped, exempt int
	seenExempt := map[string]bool{}
	for _, route := range r.Routes() {
		key := route.Method + " " + route.Path
		t.Run(key, func(t *testing.T) {
			w := do(r, idemTableProbe(route.Method, route.Path))

			// An idempotent route refuses the oversized key before its handler runs, so this
			// answer is the wrapper's fingerprint; no service is wired, so no handler could
			// produce it.
			refusedByWrapper := w.Code == http.StatusBadRequest && strings.Contains(w.Body.String(), idemTableKeyLimitDetail)
			switch {
			case route.Method == http.MethodGet:
				assert.False(t, refusedByWrapper, "a read is not idempotent-wrapped")
			case idemTableExempt[key]:
				seenExempt[key] = true
				assert.False(t, refusedByWrapper, "an exempt route is not idempotent-wrapped")
			default:
				assert.True(t, refusedByWrapper, "got %d %s", w.Code, w.Body.String())
			}
		})
		switch {
		case route.Method == http.MethodGet:
		case idemTableExempt[key]:
			exempt++
		default:
			wrapped++
		}
	}

	require.Len(t, seenExempt, len(idemTableExempt), "every exempt route is registered")
	assert.Equal(t, 15, wrapped, "the idempotent write routes of the public API")
	assert.Equal(t, 3, exempt)
}

func TestRegisterInternalRoutes_NoRouteIsIdempotentWrapped(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.RecoveryWithWriter(io.Discard))
	httpadapter.RegisterInternalRoutes(r.Group("/api/v1/internal"), handler.New(handler.Services{Cache: newMemCache(), IdempotencyTTL: time.Hour}))
	routes := r.Routes()
	require.NotEmpty(t, routes)

	for _, route := range routes {
		t.Run(route.Method+" "+route.Path, func(t *testing.T) {
			probe := idemTableProbe(route.Method, route.Path)

			w := do(r, probe)

			refusedByWrapper := w.Code == http.StatusBadRequest && strings.Contains(w.Body.String(), idemTableKeyLimitDetail)
			assert.False(t, refusedByWrapper, "internal routes do not use the idempotency wrapper: %d %s", w.Code, w.Body.String())
		})
	}
}
