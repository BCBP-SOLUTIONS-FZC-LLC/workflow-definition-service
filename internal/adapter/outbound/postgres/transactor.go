package postgres

import (
	"context"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgcommon"
	"github.com/jackc/pgx/v5"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

var _ port.Transactor = (*Transactor)(nil)

type Transactor struct {
	pool *pgcommon.Pool
}

func NewTransactor(pool *pgcommon.Pool) *Transactor {
	return &Transactor{pool: pool}
}

func (t *Transactor) RunInTx(ctx context.Context, fn func(context.Context) error) error {
	return pgcommon.RunInTx(ctx, t.pool, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error { //nolint:wrapcheck
		return fn(withTx(ctx, tx))
	})
}
