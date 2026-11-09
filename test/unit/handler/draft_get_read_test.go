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

func TestGetDraft_DatabaseUnavailable_Returns503UpstreamUnavailable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
	}{
		{name: "bare sentinel", err: domain.ErrUpstreamUnavailable},
		{name: "wrapped by the service", err: fmt.Errorf("get draft: %w", domain.ErrUpstreamUnavailable)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{
				get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
					return nil, tc.err
				},
			}, &fakeVersionSvc{}, &fakeValidationSvc{})

			w := do(newRouter(h), req(http.MethodGet, "/api/v1/workflows/"+testWFID.String()+"/draft", nil))

			require.Equal(t, http.StatusServiceUnavailable, w.Code)
			prob := decodeProblem(t, w)
			assert.Equal(t, "UPSTREAM_UNAVAILABLE", prob["code"])
		})
	}
}
