package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestConnectorService_WriteRestAlias_BaseURLPathOnAllowedHost_StoredUnchanged(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
	}{
		{name: "path", baseURL: "http://tender-service.internal/v1/tenders"},
		{name: "path and port", baseURL: "http://tender-service.internal:8080/v1/tenders"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo := &fakeAliasRepo{}
			svc := service.NewConnectorService(service.ConnectorDeps{
				Aliases:           repo,
				AliasAllowedHosts: []string{"tender-service.internal"},
			})

			err := svc.WriteRestAlias(context.Background(), domain.ConnectorRestAlias{
				Alias: "tender-get", Method: "GET", BaseURL: tc.baseURL, PathTemplate: "/x",
			})

			require.NoError(t, err)
			require.Len(t, repo.upserts, 1)
			assert.Equal(t, tc.baseURL, repo.upserts[0].BaseURL)
		})
	}
}
