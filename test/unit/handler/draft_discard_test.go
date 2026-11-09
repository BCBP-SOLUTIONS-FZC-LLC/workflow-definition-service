package handler_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscardDraft_CallerAndIDVariants_Returns204(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		roles string
		id    string
	}{
		{name: "tenant_owner role", roles: "tenant_owner", id: testWFID.String()},
		{name: "id without hyphens", roles: "tenant_admin", id: hexWithoutHyphens(testWFID)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var gotTenant, gotWorkflow uuid.UUID
			h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{
				discard: func(_ context.Context, tenantID, workflowID uuid.UUID) error {
					gotTenant, gotWorkflow = tenantID, workflowID
					return nil
				},
			}, &fakeVersionSvc{}, &fakeValidationSvc{})

			r := req(http.MethodDelete, "/api/v1/workflows/"+tc.id+"/draft", nil)
			r.Header.Set("x-tenant-roles", tc.roles)
			w := do(newRouter(h), r)

			assert.Equal(t, http.StatusNoContent, w.Code)
			assert.Empty(t, w.Body.String())
			assert.Equal(t, testTenantID, gotTenant)
			assert.Equal(t, testWFID, gotWorkflow)
		})
	}
}

func TestDiscardDraft_UnclassifiedServiceError_Returns500(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
	}{
		{
			name: "follow-up version lookup failed",
			err:  errors.New("delete draft: scan workflow version: unexpected column type"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{
				discard: func(_ context.Context, _, _ uuid.UUID) error { return tc.err },
			}, &fakeVersionSvc{}, &fakeValidationSvc{})

			w := do(newRouter(h), adminReq(http.MethodDelete,
				"/api/v1/workflows/"+testWFID.String()+"/draft", nil))

			require.Equal(t, http.StatusInternalServerError, w.Code)
			prob := decodeProblem(t, w)
			assert.Equal(t, "INTERNAL_ERROR", prob["code"])
			assert.NotContains(t, w.Body.String(), "unexpected column type")
		})
	}
}

func hexWithoutHyphens(id uuid.UUID) string {
	return strings.ReplaceAll(id.String(), "-", "")
}
