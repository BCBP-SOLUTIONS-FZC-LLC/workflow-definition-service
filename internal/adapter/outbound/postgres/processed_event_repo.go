package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres/db"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

var _ port.ProcessedEventRepository = (*ProcessedEventRepo)(nil)

type ProcessedEventRepo struct {
	q *db.Queries
}

func NewProcessedEventRepo(pool *pgxpool.Pool) *ProcessedEventRepo {
	return &ProcessedEventRepo{q: db.New(pool)}
}

func (r *ProcessedEventRepo) RecordIfNew(_ context.Context, _, _ uuid.UUID, _ string) (bool, error) {
	return false, errors.New("not implemented")
}

func (r *ProcessedEventRepo) PruneOlderThan(_ context.Context, _ int) error {
	return errors.New("not implemented")
}
