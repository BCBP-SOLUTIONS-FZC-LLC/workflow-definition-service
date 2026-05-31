package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgcommon"

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
