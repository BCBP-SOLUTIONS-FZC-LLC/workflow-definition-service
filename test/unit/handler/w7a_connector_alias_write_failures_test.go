package handler_test

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

const w7aAliasPath = "/api/v1/internal/connector-aliases/rest"

func TestWriteConnectorRestAlias_BadPathTemplateAndForeignHostTogether_400ReportsThePathTemplateOnly(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		pathTemplate string
		wantDetail   string
	}{
		{name: "parent segment", pathTemplate: "/w7a/../x", wantDetail: "pathTemplate must not contain //, .., @ or a backslash"},
		{name: "no leading slash", pathTemplate: "w7a/x", wantDetail: "pathTemplate must start with /"},
		{name: "whitespace", pathTemplate: "/w7a x", wantDetail: "pathTemplate must not contain whitespace or control characters"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			repo := mocks.NewMockConnectorAliasRepository(gomock.NewController(t))
			svc := service.NewConnectorService(service.ConnectorDeps{Aliases: repo, AliasAllowedHosts: []string{"w7a-tender.internal"}})
			body := map[string]any{"alias": "w7a-alias", "method": "GET", "baseURL": "https://w7a-evil.example.com", "pathTemplate": tc.pathTemplate}

			w := do(aliasRouter(handler.New(handler.Services{Connectors: svc})), req(http.MethodPost, w7aAliasPath, body))

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			assert.Equal(t, "application/problem+json", w.Header().Get("Content-Type"))
			prob := decodeProblem(t, w)
			assert.Equal(t, "https://api.workflow.platform/errors/bad-request", prob["type"])
			assert.Equal(t, "INVALID_INPUT", prob["code"])
			assert.Contains(t, prob["detail"], tc.wantDetail)
			assert.NotContains(t, prob["detail"], "allowed internal host")
			assert.NotContains(t, w.Body.String(), "w7a-evil.example.com")
		})
	}
}

func TestWriteConnectorRestAlias_StoreFailsWithAnUnknownError_500GenericProblemAndNoWrittenLine(t *testing.T) {
	t.Parallel()

	const driverText = "w7a insert refused: disk quota exceeded"
	ctrl := gomock.NewController(t)
	repo := mocks.NewMockConnectorAliasRepository(ctrl)
	repo.EXPECT().UpsertRest(gomock.Any(), domain.ConnectorRestAlias{
		Alias: "w7a-alias", Method: "GET", BaseURL: "https://w7a-tender.internal", PathTemplate: "/w7a/x", Timeout: 5 * time.Second,
	}).Return(errors.New(driverText))
	log := &w7aLog{}
	svc := service.NewConnectorService(service.ConnectorDeps{Aliases: repo, AliasAllowedHosts: []string{"w7a-tender.internal"}, Log: log})
	body := map[string]any{"alias": "w7a-alias", "method": "GET", "baseURL": "https://w7a-tender.internal", "pathTemplate": "/w7a/x"}

	w := do(aliasRouter(handler.New(handler.Services{Connectors: svc, Log: log})), req(http.MethodPost, w7aAliasPath, body))

	require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
	assert.Equal(t, "application/problem+json", w.Header().Get("Content-Type"))
	prob := decodeProblem(t, w)
	assert.Equal(t, "https://api.workflow.platform/errors/internal-error", prob["type"])
	assert.Equal(t, "INTERNAL_ERROR", prob["code"])
	assert.Equal(t, "an unexpected error occurred", prob["detail"])
	assert.NotContains(t, w.Body.String(), driverText)
	assert.NotContains(t, log.infos, "connector alias written")
	logged := log.loggedErrors()
	require.Len(t, logged, 1)
	assert.Contains(t, logged[0]["error"], driverText)
}
