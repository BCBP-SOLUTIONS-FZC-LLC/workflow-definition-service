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

type coreSvcAFailingAliasRepo struct {
	err error
}

func (r coreSvcAFailingAliasRepo) ListRest(context.Context) ([]domain.ConnectorRestAlias, error) {
	return nil, r.err
}

func (r coreSvcAFailingAliasRepo) UpsertRest(context.Context, domain.ConnectorRestAlias) error {
	return r.err
}

func (r coreSvcAFailingAliasRepo) DeleteRest(context.Context, string) (bool, error) {
	return false, r.err
}

func TestConnectorService_AliasStoreFailure_SurfacesTheErrorAndWritesNoAuditLine(t *testing.T) {
	t.Parallel()

	storeDown := errors.New("alias table unreachable")
	alias := domain.ConnectorRestAlias{Alias: "crm", Method: "POST", BaseURL: "http://crm.internal", PathTemplate: "/v1/x"}
	cases := []struct {
		name string
		run  func(svc *service.ConnectorService) error
	}{
		{"list", func(svc *service.ConnectorService) error {
			got, err := svc.ListAliases(context.Background())
			assert.Nil(t, got)
			return err
		}},
		{"write", func(svc *service.ConnectorService) error {
			return svc.WriteRestAlias(context.Background(), alias)
		}},
		{"delete", func(svc *service.ConnectorService) error {
			found, err := svc.DeleteRestAlias(context.Background(), "crm")
			assert.False(t, found, "a failed delete does not report the alias as found")
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			log := &coreSvcALog{}
			svc := service.NewConnectorService(service.ConnectorDeps{
				Aliases: coreSvcAFailingAliasRepo{err: storeDown}, Log: log, AliasAllowedHosts: []string{"crm.internal"},
			})

			err := tc.run(svc)

			require.ErrorIs(t, err, storeDown)
			assert.Empty(t, log.infos, "no write or delete is logged as done")
		})
	}
}
