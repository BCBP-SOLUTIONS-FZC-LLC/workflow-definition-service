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

type recordingAliasRepo struct {
	upserts int
}

func (*recordingAliasRepo) ListRest(context.Context) ([]domain.ConnectorRestAlias, error) {
	return nil, nil
}

func (r *recordingAliasRepo) UpsertRest(context.Context, domain.ConnectorRestAlias) error {
	r.upserts++
	return nil
}

func (*recordingAliasRepo) DeleteRest(context.Context, string) (bool, error) { return false, nil }

func TestConnectorService_WriteRestAlias_BaseURLWithQueryOrFragment_IsRefusedAndNotStoredOrLogged(t *testing.T) {
	t.Parallel()

	for _, baseURL := range []string{
		"https://api.internal/v1?token=secret",
		"https://api.internal/v1#frag",
	} {
		t.Run(baseURL, func(t *testing.T) {
			t.Parallel()

			repo := &recordingAliasRepo{}
			log := &sgCAuditLog{}
			svc := service.NewConnectorService(service.ConnectorDeps{
				Aliases: repo, Log: log, AliasAllowedHosts: []string{"api.internal"},
			})

			err := svc.WriteRestAlias(context.Background(), domain.ConnectorRestAlias{
				Alias: "crm", Method: "GET", BaseURL: baseURL, PathTemplate: "/x",
			})

			require.Error(t, err)
			assert.True(t, errors.Is(err, domain.ErrInvalidConnectorAliasInput))
			assert.Zero(t, repo.upserts)
			assert.Empty(t, log.snapshot(), "the refused URL is not logged")
		})
	}
}
