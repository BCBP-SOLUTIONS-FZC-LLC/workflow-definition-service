package postgres

import (
	"context"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres/db"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

var _ port.ConnectorAliasRepository = (*ConnectorAliasRepo)(nil)

type ConnectorAliasRepo struct {
	pool *pgcommon.Pool
}

func NewConnectorAliasRepo(pool *pgcommon.Pool) *ConnectorAliasRepo {
	return &ConnectorAliasRepo{pool: pool}
}

func (r *ConnectorAliasRepo) ListRest(ctx context.Context) ([]domain.ConnectorRestAlias, error) {
	var out []domain.ConnectorRestAlias
	err := exec(ctx, r.pool, func(dbtx db.DBTX) error {
		rows, err := db.New(dbtx).ListRestAliases(ctx)
		if err != nil {
			return mapErr(err)
		}
		out = make([]domain.ConnectorRestAlias, len(rows))
		for i, row := range rows {
			out[i] = domain.ConnectorRestAlias{
				Alias:        row.Alias,
				Method:       row.Method,
				BaseURL:      row.BaseUrl,
				PathTemplate: row.PathTemplate,
				Timeout:      time.Duration(row.TimeoutMs) * time.Millisecond,
			}
		}
		return nil
	})
	return out, err
}

func (r *ConnectorAliasRepo) UpsertRest(ctx context.Context, a domain.ConnectorRestAlias) error {
	return exec(ctx, r.pool, func(dbtx db.DBTX) error {
		return mapErr(db.New(dbtx).UpsertRestAlias(ctx, db.UpsertRestAliasParams{
			Alias:        a.Alias,
			Method:       a.Method,
			BaseUrl:      a.BaseURL,
			PathTemplate: a.PathTemplate,
			TimeoutMs:    int32(a.Timeout.Milliseconds()),
		}))
	})
}

func (r *ConnectorAliasRepo) DeleteRest(ctx context.Context, alias string) (bool, error) {
	var deleted bool
	err := exec(ctx, r.pool, func(dbtx db.DBTX) error {
		n, err := db.New(dbtx).DeleteRestAlias(ctx, alias)
		if err != nil {
			return mapErr(err)
		}
		deleted = n > 0
		return nil
	})
	return deleted, err
}
