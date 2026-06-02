package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres/db"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

var _ port.OutboxRepository = (*OutboxRepo)(nil)

type OutboxRepo struct {
	pool *pgcommon.Pool
}

func NewOutboxRepo(pool *pgcommon.Pool) *OutboxRepo {
	return &OutboxRepo{pool: pool}
}

func (r *OutboxRepo) Enqueue(ctx context.Context, event *domain.OutboxEvent) error {
	err := r.pool.WithConn(ctx, func(ctx context.Context, conn *pgxpool.Conn) error {
		queries := db.New(conn)
		return queries.EnqueueEvent(ctx, db.EnqueueEventParams{
			ID:        event.ID,
			EventType: event.Topic,
			Payload:   event.PayloadJSON,
			TenantID:  event.TenantID.String(),
			TraceID:   "",
		})
	})
	if err != nil {
		return fmt.Errorf("outbox enqueue: %w", err)
	}
	return nil
}

func (r *OutboxRepo) FetchPending(_ context.Context, _ int) ([]*domain.OutboxEvent, error) {
	return nil, errors.New("not implemented")
}

func (r *OutboxRepo) MarkSent(_ context.Context, _ uuid.UUID) error {
	return errors.New("not implemented")
}

func (r *OutboxRepo) MarkFailed(_ context.Context, _ uuid.UUID, _ string) error {
	return errors.New("not implemented")
}
