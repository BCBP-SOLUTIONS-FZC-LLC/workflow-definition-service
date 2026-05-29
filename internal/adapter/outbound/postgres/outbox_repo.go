package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres/db"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

var _ port.OutboxRepository = (*OutboxRepo)(nil)

type OutboxRepo struct {
	q *db.Queries
}

func NewOutboxRepo(pool *pgxpool.Pool) *OutboxRepo {
	return &OutboxRepo{q: db.New(pool)}
}

func (r *OutboxRepo) Enqueue(_ context.Context, _ *domain.OutboxEvent) error {
	return errors.New("not implemented")
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
