package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
)

type contentTypeEventRecorder struct {
	revoked []uuid.UUID
	tenants []uuid.UUID
	events  []uuid.UUID
}

func (f *contentTypeEventRecorder) HandleDepartmentMembershipRevoked(
	_ context.Context, eventID, tenantID, userID uuid.UUID, _ string, _ time.Time,
) error {
	f.events, f.tenants, f.revoked = append(f.events, eventID), append(f.tenants, tenantID), append(f.revoked, userID)
	return nil
}

func (f *contentTypeEventRecorder) HandleTenantMembershipRevoked(
	_ context.Context, eventID, tenantID, userID uuid.UUID, _ time.Time,
) error {
	f.events, f.tenants, f.revoked = append(f.events, eventID), append(f.tenants, tenantID), append(f.revoked, userID)
	return nil
}

func TestInternalEvents_ContentTypeIsNotEnforced_ProcessedNormally(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
	}{
		{name: "plain text", contentType: "text/plain"},
		{name: "no content type", contentType: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			recorder := &contentTypeEventRecorder{}
			engine := NewRouter(RouterConfig{
				GinConfig:        testGinConfig,
				AppEnv:           "test",
				InternalAPIToken: "secret",
				Handler:          handler.New(handler.Services{Membership: recorder}),
				DB:               fakeDBPinger{health: DBHealth{Healthy: true}},
				Cache:            fakePinger{},
			}).engine

			userID, eventID, tenantID := uuid.New(), uuid.New(), uuid.New()
			body := `{"id":"` + eventID.String() + `","type":"MembershipRevoked","tenant_id":"` + tenantID.String() +
				`","time":"2026-10-01T12:00:00Z","data":{"user_id":"` + userID.String() + `"}}`
			req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/events", strings.NewReader(body))
			if tc.contentType != "" {
				req.Header.Set("Content-Type", tc.contentType)
			}
			req.Header.Set("x-internal-token", "secret")
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code, "the internal group does not enforce a JSON content type: %s", w.Body.String())
			assert.Empty(t, w.Body.String())
			assert.Equal(t, []uuid.UUID{userID}, recorder.revoked, "the event must be processed, not just acknowledged")
			assert.Equal(t, []uuid.UUID{eventID}, recorder.events)
			assert.Equal(t, []uuid.UUID{tenantID}, recorder.tenants)
		})
	}
}
