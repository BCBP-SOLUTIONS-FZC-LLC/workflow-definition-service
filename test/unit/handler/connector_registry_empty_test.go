package handler_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-connectors/v2/pkg/registry"
)

func TestListConnectorRegistry_EmptyRegistry_ReturnsEmptyObject200(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		defs map[string]registry.Definition
	}{
		{name: "nil map", defs: nil},
		{name: "empty map", defs: map[string]registry.Definition{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newConnectorHandler(&fakeConnectorSvc{
				registryFn: func(context.Context) map[string]registry.Definition { return tc.defs },
			})

			w := do(newRouter(h), req(http.MethodGet, "/api/v1/connectors/registry", nil))

			require.Equal(t, http.StatusOK, w.Code)
			assert.JSONEq(t, `{"connectors":{}}`, w.Body.String())
		})
	}
}
