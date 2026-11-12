package middleware_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/logger"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/middleware"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
)

func internalRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.RequireSystemCaller())
	r.POST("/internal/events", func(c *gin.Context) { c.Status(http.StatusOK) })
	return r
}

func doInternalReq(r *gin.Engine, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/internal/events", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestRequireSystemCaller(t *testing.T) {
	tests := []struct {
		name       string
		headers    map[string]string
		wantStatus int
	}{
		{"system caller passes", map[string]string{"x-user-id": "iam-system", "x-tenant-roles": "iam-system"}, http.StatusOK},
		{"role among several passes", map[string]string{"x-user-id": "u", "x-tenant-roles": "tenant_admin, iam-system"}, http.StatusOK},
		{"entries are trimmed", map[string]string{"x-user-id": "u", "x-tenant-roles": "  iam-system  ,x"}, http.StatusOK},
		{"no headers rejected", nil, http.StatusUnauthorized},
		{"missing user id rejected", map[string]string{"x-tenant-roles": "iam-system"}, http.StatusUnauthorized},
		{"blank user id rejected", map[string]string{"x-user-id": "   ", "x-tenant-roles": "iam-system"}, http.StatusUnauthorized},
		{"missing roles rejected", map[string]string{"x-user-id": "iam-system"}, http.StatusUnauthorized},
		{"other role rejected", map[string]string{"x-user-id": "u", "x-tenant-roles": "tenant_admin"}, http.StatusUnauthorized},
		{"role match is case sensitive", map[string]string{"x-user-id": "u", "x-tenant-roles": "IAM-System"}, http.StatusUnauthorized},
		{"role match is exact", map[string]string{"x-user-id": "u", "x-tenant-roles": "iam-system-x,not-iam-system"}, http.StatusUnauthorized},
		{"the old token header alone is rejected", map[string]string{"x-internal-token": "secret"}, http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := doInternalReq(internalRouter(), tt.headers)
			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", w.Code, tt.wantStatus)
			}
			if tt.wantStatus != http.StatusUnauthorized {
				return
			}
			var p struct {
				Code   string `json:"code"`
				Detail string `json:"detail"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
				t.Fatalf("decode problem body: %v", err)
			}
			if p.Code != "UNAUTHORIZED" || p.Detail != "missing or invalid internal caller identity" {
				t.Errorf("problem = %+v", p)
			}
		})
	}
}

func registerMiddlewareMetrics(t *testing.T) {
	t.Helper()
	log, err := logger.NewLogger("dev")
	require.NoError(t, err)
	_ = gincommon.ObservabilityMiddlewares(gincommon.Config{
		Logger: log, ServiceName: "workflow-definition-service", BuildVersion: "test",
		Domain: observability.Domain, Environment: "test",
	})
	observability.Register()
}

func TestRequireSystemCaller_ARefusedCallerIsCountedByReason(t *testing.T) {
	registerMiddlewareMetrics(t)
	tests := []struct {
		name    string
		headers map[string]string
		reason  string
	}{
		{"no headers is a missing user", nil, "missing_user"},
		{"blank user id is a missing user", map[string]string{"x-user-id": "  ", "x-tenant-roles": "iam-system"}, "missing_user"},
		{"missing roles is a missing role", map[string]string{"x-user-id": "u"}, "missing_role"},
		{"another role is a missing role", map[string]string{"x-user-id": "u", "x-tenant-roles": "tenant_admin"}, "missing_role"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			counter := observability.SystemCallerRejectedTotal.WithLabelValues(tt.reason)
			other := "missing_role"
			if tt.reason == other {
				other = "missing_user"
			}
			beforeReason := testutil.ToFloat64(counter)
			beforeOther := testutil.ToFloat64(observability.SystemCallerRejectedTotal.WithLabelValues(other))

			w := doInternalReq(internalRouter(), tt.headers)

			assert.Equal(t, http.StatusUnauthorized, w.Code)
			assert.Equal(t, beforeReason+1, testutil.ToFloat64(counter))
			assert.Equal(t, beforeOther, testutil.ToFloat64(observability.SystemCallerRejectedTotal.WithLabelValues(other)))
		})
	}
}

func TestRequireSystemCaller_AnAdmittedCallerIsNotCounted(t *testing.T) {
	registerMiddlewareMetrics(t)
	total := func() float64 {
		return testutil.ToFloat64(observability.SystemCallerRejectedTotal.WithLabelValues("missing_user")) +
			testutil.ToFloat64(observability.SystemCallerRejectedTotal.WithLabelValues("missing_role"))
	}
	before := total()

	w := doInternalReq(internalRouter(), map[string]string{"x-user-id": "iam-system", "x-tenant-roles": "iam-system"})

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, before, total())
}
