package middleware_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/middleware"
)

const identityTestContextKey = "workflow.identity"

type identityTestProblem struct {
	Type   string `json:"type"`
	Status int    `json:"status"`
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

func TestRequireIdentity_NoRequestContextAndNilLogger_Returns500WithoutPanic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reached := false
	r := gin.New()
	r.Use(middleware.RequireIdentity(nil))
	r.GET("/x", func(c *gin.Context) { reached = true; c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	require.NotPanics(t, func() {
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	})

	assert.False(t, reached, "a route without the identity chain must not serve")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	var p identityTestProblem
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &p))
	assert.Equal(t, "INTERNAL_ERROR", p.Code)
	assert.Equal(t, "https://api.workflow.platform/errors/internal-error", p.Type)
	assert.Equal(t, "missing request context", p.Detail)
}

func TestRequireIdentity_NonCanonicalUUIDForms_AcceptedAndNormalised(t *testing.T) {
	tenant := uuid.MustParse("0f8fad5b-d9cb-469f-a165-70867728950e")
	user := uuid.MustParse("7c9e6679-7425-40de-944b-e07fc1f90ae7")
	cases := []struct {
		name   string
		format func(uuid.UUID) string
	}{
		{"uppercase", func(u uuid.UUID) string { return upperASCII(u.String()) }},
		{"braces", func(u uuid.UUID) string { return "{" + u.String() + "}" }},
		{"urn prefix", func(u uuid.UUID) string { return "urn:uuid:" + u.String() }},
		{"no dashes", hexNoDashes},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			r := gin.New()
			for _, mw := range gincommon.ProtectedMiddlewares(gincommon.Config{}) {
				r.Use(mw)
			}
			r.Use(middleware.RequireIdentity(nil))
			var gotID middleware.Identity
			var gotIDOK bool
			var gotTenantGUC, gotUserGUC string
			r.GET("/x", func(c *gin.Context) {
				gotID, gotIDOK = middleware.IdentityFrom(c)
				guc, _ := pgcommon.GUCSetFromContext(c.Request.Context())
				gotTenantGUC, gotUserGUC = guc.TenantID, guc.UserID
				c.Status(http.StatusOK)
			})

			req := httptest.NewRequest(http.MethodGet, "/x", nil)
			req.Header.Set("x-tenant-id", tc.format(tenant))
			req.Header.Set("x-user-id", tc.format(user))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			require.True(t, gotIDOK)
			assert.Equal(t, tenant, gotID.TenantID)
			assert.Equal(t, user, gotID.UserID)
			assert.Equal(t, tenant.String(), gotTenantGUC)
			assert.Equal(t, user.String(), gotUserGUC)
		})
	}
}

func TestIdentityFrom_NoIdentityOrWrongType_ReturnsZeroAndFalse(t *testing.T) {
	cases := []struct {
		name string
		set  func(c *gin.Context)
	}{
		{"key absent", func(*gin.Context) {}},
		{"key holds a string", func(c *gin.Context) { c.Set(identityTestContextKey, "not-an-identity") }},
		{"key holds a pointer to Identity", func(c *gin.Context) {
			c.Set(identityTestContextKey, &middleware.Identity{TenantID: uuid.New()})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			tc.set(c)

			got, ok := middleware.IdentityFrom(c)

			assert.False(t, ok)
			assert.Equal(t, middleware.Identity{}, got)
		})
	}
}

func upperASCII(s string) string {
	b := []byte(s)
	for i, ch := range b {
		if ch >= 'a' && ch <= 'z' {
			b[i] = ch - 'a' + 'A'
		}
	}
	return string(b)
}

func hexNoDashes(u uuid.UUID) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, 32)
	for _, b := range u {
		out = append(out, digits[b>>4], digits[b&0x0f])
	}
	return string(out)
}
