package handler_test

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestIdempotentAdminRoutes_NonAdminWithKey_Gets403WithoutTouchingTheCache(t *testing.T) {
	wf := testWFID.String()
	ver := testVerID.String()
	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"create workflow", http.MethodPost, "/api/v1/workflows"},
		{"archive workflow", http.MethodPost, "/api/v1/workflows/" + wf + "/archive"},
		{"init draft", http.MethodPost, "/api/v1/workflows/" + wf + "/draft"},
		{"update draft", http.MethodPut, "/api/v1/workflows/" + wf + "/draft"},
		{"discard draft", http.MethodDelete, "/api/v1/workflows/" + wf + "/draft"},
		{"publish version", http.MethodPost, "/api/v1/workflows/" + wf + "/versions/" + ver + "/publish"},
		{"clone version", http.MethodPost, "/api/v1/workflows/" + wf + "/versions/" + ver + "/clone"},
		{"promote version", http.MethodPost, "/api/v1/workflows/" + wf + "/versions/" + ver + "/promote"},
		{"write connector credential", http.MethodPost, "/api/v1/connectors/credentials"},
		{"create module", http.MethodPost, "/api/v1/modules"},
		{"add module version", http.MethodPost, "/api/v1/modules/" + wf + "/versions"},
		{"publish module version", http.MethodPost, "/api/v1/modules/" + wf + "/versions/" + ver + "/publish"},
		{"archive module", http.MethodDelete, "/api/v1/modules/" + wf},
		{"create starter", http.MethodPost, "/api/v1/starters"},
		{"create starter from workflow version", http.MethodPost, "/api/v1/starters/from-workflow-version/" + ver},
		{"delete starter", http.MethodDelete, "/api/v1/starters/" + wf},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cacheCalls atomic.Int32
			cache := &fakeCache{
				get: func(context.Context, string) (string, error) { cacheCalls.Add(1); return "", nil },
				set: func(context.Context, string, string, time.Duration) error { cacheCalls.Add(1); return nil },
				setnx: func(context.Context, string, string, time.Duration) (bool, error) {
					cacheCalls.Add(1)
					return true, nil
				},
				delIfEqual: func(context.Context, string, string) error { cacheCalls.Add(1); return nil },
			}
			r := newRouter(handler.New(handler.Services{Cache: cache, IdempotencyTTL: time.Hour}))

			httpReq := req(tt.method, tt.path, map[string]string{})
			httpReq.Header.Set("Idempotency-Key", "non-admin-key")
			w := do(r, httpReq)

			assert.Equal(t, http.StatusForbidden, w.Code)
			assert.Contains(t, w.Body.String(), "FORBIDDEN")
			assert.Zero(t, cacheCalls.Load(), "the role check comes before any idempotency cache access")
		})
	}
}

func TestIdempotentAdminRoutes_FormerAdminReplay_Gets403NotTheStoredSuccess(t *testing.T) {
	var created atomic.Int32
	wfSvc := &fakeWorkflowSvc{
		create: func(context.Context, uuid.UUID, uuid.UUID, string, string, string, string) (*domain.Workflow, *domain.WorkflowVersion, error) {
			created.Add(1)
			return newWorkflow(), newDraftVersion(), nil
		},
	}
	cache := newMemCache()
	r := newRouter(handler.New(handler.Services{
		Workflows: wfSvc, Cache: (*fakeCache)(nil).wrap(cache), IdempotencyTTL: time.Hour,
	}))
	body := map[string]string{"key": "k", "name": "n", "description": "d", "bpmn_xml": "<x/>"}

	asAdmin := adminReq(http.MethodPost, "/api/v1/workflows", body)
	asAdmin.Header.Set("Idempotency-Key", "former-admin")
	require.Equal(t, http.StatusCreated, do(r, asAdmin).Code)

	asDemoted := req(http.MethodPost, "/api/v1/workflows", body)
	asDemoted.Header.Set("Idempotency-Key", "former-admin")
	w := do(r, asDemoted)

	assert.Equal(t, http.StatusForbidden, w.Code, "a caller who lost the admin role must not receive the stored success")
	assert.Equal(t, int32(1), created.Load())
	assert.Empty(t, pendingKeys(cache))
}
