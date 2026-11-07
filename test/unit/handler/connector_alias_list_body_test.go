package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestListConnectorAliases_StoredAliases_BodyShape(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		aliases     []domain.ConnectorRestAlias
		wantRestRaw string
	}{
		{
			name:        "nil list is an empty array not null",
			aliases:     nil,
			wantRestRaw: `[]`,
		},
		{
			name:        "empty list is an empty array not null",
			aliases:     []domain.ConnectorRestAlias{},
			wantRestRaw: `[]`,
		},
		{
			name: "five second timeout is written in nanoseconds",
			aliases: []domain.ConnectorRestAlias{{
				Alias: "tender-get", Method: "GET",
				BaseURL: "http://tender-service.internal", PathTemplate: "/x",
				Timeout: 5 * time.Second,
			}},
			wantRestRaw: `[{"alias":"tender-get","method":"GET","baseURL":"http://tender-service.internal","pathTemplate":"/x","timeout":5000000000}]`,
		},
		{
			name: "zero timeout is written as zero",
			aliases: []domain.ConnectorRestAlias{{
				Alias: "tender-get", Method: "GET",
				BaseURL: "http://tender-service.internal", PathTemplate: "/x",
				Timeout: 0,
			}},
			wantRestRaw: `[{"alias":"tender-get","method":"GET","baseURL":"http://tender-service.internal","pathTemplate":"/x","timeout":0}]`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			conn := &fakeConnectorSvc{
				listAliasesFn: func(context.Context) ([]domain.ConnectorRestAlias, error) {
					return tc.aliases, nil
				},
			}
			w := do(aliasRouter(handler.New(handler.Services{Connectors: conn})),
				req(http.MethodGet, "/api/v1/internal/connector-aliases", nil))
			require.Equal(t, http.StatusOK, w.Code)

			var body map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			assert.JSONEq(t, `1`, string(body["version"]))
			assert.JSONEq(t, tc.wantRestRaw, string(body["restCall"]))
		})
	}
}
