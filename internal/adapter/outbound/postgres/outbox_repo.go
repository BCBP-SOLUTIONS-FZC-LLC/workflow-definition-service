package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/pkg/events"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/pkg/outbox"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

var _ port.OutboxRepository = (*OutboxRepo)(nil)

type OutboxRepo struct {
	pool *pgcommon.Pool
}

func NewOutboxRepo(pool *pgcommon.Pool) *OutboxRepo {
	return &OutboxRepo{pool: pool}
}

// Enqueue writes env to the outbox_events table within the transaction stored
// in ctx. The caller must be inside a Transactor.RunInTx callback; enqueueing
// outside a transaction is rejected because the outbox insert and the business
// write must be atomic.
func (r *OutboxRepo) Enqueue(ctx context.Context, env events.Envelope[json.RawMessage]) error {
	tx, ok := txFromContext(ctx)
	if !ok {
		return errors.New("outbox: Enqueue must be called inside a transaction — use Transactor.RunInTx")
	}
	return outbox.Enqueue(ctx, tx, env) //nolint:wrapcheck
}
