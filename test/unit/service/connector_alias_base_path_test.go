package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestConnectorService_WriteRestAlias_BasePathIsStoredAsSent(t *testing.T) {
	repo := &fakeAliasRepo{}
	svc := service.NewConnectorService(service.ConnectorDeps{Aliases: repo, AliasAllowedHosts: []string{"tender-service.internal"}})

	err := svc.WriteRestAlias(context.Background(), domain.ConnectorRestAlias{
		Alias: "tender-get", Method: "GET", BaseURL: "https://tender-service.internal/v2/", PathTemplate: "/x",
	})

	require.NoError(t, err)
	require.Len(t, repo.upserts, 1)
	assert.Equal(t, "https://tender-service.internal/v2/", repo.upserts[0].BaseURL)
}

func TestConnectorService_WriteRestAlias_BasePathThatClimbsOut_IsRefusedBeforeTheRepository(t *testing.T) {
	for _, baseURL := range []string{
		"https://tender-service.internal/../x",
		"https://tender-service.internal//x",
		"https://tender-service.internal/a@b",
	} {
		t.Run(baseURL, func(t *testing.T) {
			repo := &fakeAliasRepo{}
			svc := service.NewConnectorService(service.ConnectorDeps{Aliases: repo, AliasAllowedHosts: []string{"tender-service.internal"}})

			err := svc.WriteRestAlias(context.Background(), domain.ConnectorRestAlias{
				Alias: "tender-get", Method: "GET", BaseURL: baseURL, PathTemplate: "/x",
			})

			require.Error(t, err)
			assert.True(t, errors.Is(err, domain.ErrInvalidConnectorAliasInput))
			assert.Empty(t, repo.upserts)
		})
	}
}
