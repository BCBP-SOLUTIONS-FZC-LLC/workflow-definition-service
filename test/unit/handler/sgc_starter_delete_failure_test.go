package handler_test

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

type sgCLoggedLine struct {
	msg    string
	fields map[string]any
}

type sgCLog struct {
	mu     sync.Mutex
	errors []sgCLoggedLine
}

func (l *sgCLog) Error(msg string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errors = append(l.errors, sgCLoggedLine{msg: msg, fields: fields})
}
func (*sgCLog) Debug(string, map[string]any) {}
func (*sgCLog) Info(string, map[string]any)  {}
func (*sgCLog) Warn(string, map[string]any)  {}
func (*sgCLog) Fatal(string, map[string]any) {}

func TestDeleteStarter_UnclassifiedDeleteError_Returns500WithGenericBodyAndLogsTheError(t *testing.T) {
	t.Parallel()

	const driverText = "pq: deadlock detected while deleting row"
	cases := []struct {
		name   string
		delErr error
	}{
		{name: "plain delete error", delErr: errors.New(driverText)},
		{name: "wrapped delete error", delErr: errors.Join(errors.New("delete starter"), errors.New(driverText))},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var gotTenants, gotIDs, deletedTenants, deletedIDs []uuid.UUID
			log := &sgCLog{}
			h := handler.New(handler.Services{
				Starters: &fakeStarterSvc{
					get: func(_ context.Context, tenantID, id uuid.UUID) (*domain.StarterTemplate, error) {
						gotTenants = append(gotTenants, tenantID)
						gotIDs = append(gotIDs, id)
						return &domain.StarterTemplate{ID: id, Scope: domain.ScopeTenant}, nil
					},
					del: func(_ context.Context, tenantID, id uuid.UUID) error {
						deletedTenants = append(deletedTenants, tenantID)
						deletedIDs = append(deletedIDs, id)
						return tc.delErr
					},
				},
				Log: log,
			})

			w := do(newRouter(h), adminReq(http.MethodDelete, "/api/v1/starters/"+testWFID.String(), nil))

			require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, "INTERNAL_ERROR", prob["code"])
			assert.EqualValues(t, http.StatusInternalServerError, prob["status"])
			assert.Equal(t, "an unexpected error occurred", prob["detail"])
			assert.NotContains(t, prob, "starter_id")
			assert.NotContains(t, prob, "message")
			assert.NotContains(t, w.Body.String(), "deadlock")
			assert.NotContains(t, w.Body.String(), "Starter deleted")

			assert.Equal(t, []uuid.UUID{testTenantID}, gotTenants)
			assert.Equal(t, []uuid.UUID{testWFID}, gotIDs)
			assert.Equal(t, []uuid.UUID{testTenantID}, deletedTenants)
			assert.Equal(t, []uuid.UUID{testWFID}, deletedIDs)

			require.Len(t, log.errors, 1)
			line := log.errors[0]
			assert.Equal(t, "request failed", line.msg)
			assert.Equal(t, http.StatusInternalServerError, line.fields["status"])
			assert.Equal(t, "INTERNAL_ERROR", line.fields["code"])
			assert.Equal(t, testTenantID.String(), line.fields["tenant_id"])
			assert.Equal(t, testUserID.String(), line.fields["user_id"])
			assert.Equal(t, "/api/v1/starters/"+testWFID.String(), line.fields["path"])
			assert.Contains(t, line.fields["error"], "deadlock")
		})
	}
}
