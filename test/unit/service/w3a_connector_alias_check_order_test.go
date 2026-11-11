package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestConnectorService_WriteRestAlias_TheShapeErrorComesBeforeTheHostError_AndNothingIsStored(t *testing.T) {
	cases := []struct {
		name        string
		method      string
		wantMessage string
		notMessage  string
	}{
		{name: "bad method and foreign host", method: "NOPE", wantMessage: "invalid method", notMessage: "not an allowed internal host"},
		{name: "foreign host only", method: "GET", wantMessage: "not an allowed internal host", notMessage: "invalid method"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo := &fakeAliasRepo{}
			log := &coreSvcALog{}
			svc := service.NewConnectorService(service.ConnectorDeps{
				Aliases:           repo,
				Log:               log,
				AliasAllowedHosts: []string{"tender-service.internal"},
			})

			err := svc.WriteRestAlias(context.Background(), domain.ConnectorRestAlias{
				Alias: "tender-get", Method: tc.method, BaseURL: "https://evil.example.com", PathTemplate: "/x",
			})

			require.ErrorIs(t, err, domain.ErrInvalidConnectorAliasInput)
			assert.Contains(t, err.Error(), tc.wantMessage)
			assert.NotContains(t, err.Error(), tc.notMessage)
			assert.Empty(t, repo.upserts, "a refused alias is never stored")
			assert.Empty(t, log.infos, "a refused alias is not audited as written")
		})
	}
}
