package handler_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestListModuleUsages_StoreUnavailable_Returns503(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{
			name: "module lookup",
			err:  fmt.Errorf("get module: %w", fmt.Errorf("%w: connection refused", domain.ErrUpstreamUnavailable)),
		},
		{
			name: "active version lookup",
			err:  fmt.Errorf("get active module version: %w", fmt.Errorf("%w: pool closed", domain.ErrUpstreamUnavailable)),
		},
		{
			name: "usages query",
			err:  fmt.Errorf("list module usages: %w", fmt.Errorf("%w: context canceled", domain.ErrUpstreamUnavailable)),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var gotTenant, gotModule uuid.UUID
			h := newModuleHandler(&fakeModuleSvc{
				usages: func(_ context.Context, tenantID, moduleID uuid.UUID) ([]*domain.ModuleUsage, error) {
					gotTenant, gotModule = tenantID, moduleID
					return nil, tc.err
				},
			})

			w := do(newRouter(h), req(http.MethodGet, "/api/v1/modules/"+testWFID.String()+"/usages", nil))

			require.Equal(t, http.StatusServiceUnavailable, w.Code)
			assert.Equal(t, "UPSTREAM_UNAVAILABLE", decodeProblem(t, w)["code"])
			assert.Equal(t, testTenantID, gotTenant)
			assert.Equal(t, testWFID, gotModule)
		})
	}
}
