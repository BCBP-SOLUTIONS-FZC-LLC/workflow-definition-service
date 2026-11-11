package handler_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

func TestListStarters_StoreFailure_Returns500WithGenericBodyAndNoList(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
	}{
		{name: "count failure", err: fmt.Errorf("list starters: %w", errors.New("list starters count: permission denied for table workflow_template"))},
		{name: "data query failure", err: fmt.Errorf("list starters: %w", errors.New("list starters: syntax error near LIMIT"))},
		{name: "row scan failure", err: fmt.Errorf("list starters: %w", errors.New("list starters scan: cannot scan NULL into *string"))},
		{name: "row iteration failure", err: fmt.Errorf("list starters: %w", errors.New("list starters rows: unexpected EOF"))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var gotTenants []uuid.UUID
			log := &sgCLog{}
			h := handler.New(handler.Services{
				Starters: &fakeStarterSvc{
					list: func(_ context.Context, tenantID uuid.UUID, _ port.StarterFilter) ([]*domain.StarterTemplate, int64, error) {
						gotTenants = append(gotTenants, tenantID)
						return []*domain.StarterTemplate{{ID: uuid.New()}}, 1, tc.err
					},
				},
				Log: log,
			})

			w := do(newRouter(h), req(http.MethodGet, "/api/v1/starters", nil))

			require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, "INTERNAL_ERROR", prob["code"])
			assert.Equal(t, "an unexpected error occurred", prob["detail"])
			assert.NotContains(t, prob, "starters")
			assert.NotContains(t, prob, "pagination")
			assert.NotContains(t, w.Body.String(), "workflow_template")
			assert.NotContains(t, w.Body.String(), "scan")
			assert.Equal(t, []uuid.UUID{testTenantID}, gotTenants)

			require.Len(t, log.errors, 1)
			assert.Equal(t, "request failed", log.errors[0].msg)
			assert.Equal(t, "INTERNAL_ERROR", log.errors[0].fields["code"])
			assert.Equal(t, testTenantID.String(), log.errors[0].fields["tenant_id"])
			assert.Contains(t, log.errors[0].fields["error"], "list starters")
		})
	}
}
