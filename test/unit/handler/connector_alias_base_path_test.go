package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestWriteConnectorRestAlias_RealService_BasePath(t *testing.T) {
	cases := []struct {
		name       string
		baseURL    string
		wantStatus int
	}{
		{"an ordinary base path is stored", "https://tender-service.internal/v2", http.StatusNoContent},
		{"a base path with .. is a 400", "https://tender-service.internal/../x", http.StatusBadRequest},
		{"a base path with // is a 400", "https://tender-service.internal/v1//x", http.StatusBadRequest},
		{"a base path with @ is a 400", "https://tender-service.internal/a@b", http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := mocks.NewMockConnectorAliasRepository(ctrl)
			if tc.wantStatus == http.StatusNoContent {
				repo.EXPECT().UpsertRest(gomock.Any(), gomock.AssignableToTypeOf(domain.ConnectorRestAlias{})).Return(nil)
			}
			svc := service.NewConnectorService(service.ConnectorDeps{Aliases: repo, AliasAllowedHosts: []string{"tender-service.internal"}})
			body := map[string]any{"alias": "tender-get", "method": "GET", "baseURL": tc.baseURL, "pathTemplate": "/x"}

			w := do(aliasRouter(handler.New(handler.Services{Connectors: svc})), req(http.MethodPost, "/api/v1/internal/connector-aliases/rest", body))

			require.Equal(t, tc.wantStatus, w.Code, w.Body.String())
			if tc.wantStatus == http.StatusBadRequest {
				assert.Equal(t, "INVALID_INPUT", decodeProblem(t, w)["code"])
			}
		})
	}
}
