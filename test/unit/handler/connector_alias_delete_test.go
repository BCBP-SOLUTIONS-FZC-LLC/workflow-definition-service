package handler_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestDeleteConnectorRestAlias_RepeatedDelete_SecondIsNotFound(t *testing.T) {
	cases := []struct {
		name  string
		alias string
	}{
		{name: "same alias deleted twice", alias: "tender-get"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stored := map[string]bool{tc.alias: true}
			var asked []string
			conn := &fakeConnectorSvc{
				deleteRestFn: func(_ context.Context, alias string) (bool, error) {
					asked = append(asked, alias)
					found := stored[alias]
					delete(stored, alias)
					return found, nil
				},
			}
			router := aliasRouter(handler.New(handler.Services{Connectors: conn}))
			path := "/api/v1/internal/connector-aliases/rest/" + tc.alias

			first := do(router, req(http.MethodDelete, path, nil))
			second := do(router, req(http.MethodDelete, path, nil))

			assert.Equal(t, http.StatusNoContent, first.Code)
			assert.Empty(t, first.Body.String())
			require.Equal(t, http.StatusNotFound, second.Code, second.Body.String())
			prob := decodeProblem(t, second)
			assert.Equal(t, "NOT_FOUND", prob["code"])
			assert.Equal(t, "alias not found", prob["detail"])
			assert.Equal(t, []string{tc.alias, tc.alias}, asked)
		})
	}
}

func TestDeleteConnectorRestAlias_FailureOrNoMatch_ReturnsProblem(t *testing.T) {
	cases := []struct {
		name       string
		path       string
		wantAlias  string
		svcErr     error
		wantStatus int
		wantCode   string
		wantDetail string
	}{
		{
			name:       "sql characters in the alias are passed through as plain text",
			path:       "/api/v1/internal/connector-aliases/rest/%27%20OR%201%3D1",
			wantAlias:  "' OR 1=1",
			wantStatus: http.StatusNotFound,
			wantCode:   "NOT_FOUND",
			wantDetail: "alias not found",
		},
		{
			name:       "database unavailable",
			path:       "/api/v1/internal/connector-aliases/rest/tender-get",
			wantAlias:  "tender-get",
			svcErr:     fmt.Errorf("%w: dial tcp 10.0.0.5:5432", domain.ErrUpstreamUnavailable),
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "UPSTREAM_UNAVAILABLE",
			wantDetail: "an upstream dependency is temporarily unavailable",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var asked []string
			conn := &fakeConnectorSvc{
				deleteRestFn: func(_ context.Context, alias string) (bool, error) {
					asked = append(asked, alias)
					return false, tc.svcErr
				},
			}

			w := do(aliasRouter(handler.New(handler.Services{Connectors: conn})), req(http.MethodDelete, tc.path, nil))

			require.Equal(t, tc.wantStatus, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, tc.wantCode, prob["code"])
			assert.Equal(t, tc.wantDetail, prob["detail"])
			assert.NotContains(t, w.Body.String(), "10.0.0.5")
			assert.Equal(t, []string{tc.wantAlias}, asked)
		})
	}
}
