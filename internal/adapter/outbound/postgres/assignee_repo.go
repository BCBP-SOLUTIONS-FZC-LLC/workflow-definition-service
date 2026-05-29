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

var _ port.AssigneeRepository = (*AssigneeRepo)(nil)

type AssigneeRepo struct {
	q *db.Queries
}

func NewAssigneeRepo(pool *pgxpool.Pool) *AssigneeRepo {
	return &AssigneeRepo{q: db.New(pool)}
}

func (r *AssigneeRepo) BulkInsert(_ context.Context, _, _ uuid.UUID, _ []*domain.NodeAssignee) error {
	return errors.New("not implemented")
}

func (r *AssigneeRepo) ListByUser(_ context.Context, _, _ uuid.UUID) ([]*domain.NodeAssignee, error) {
	return nil, errors.New("not implemented")
}

func (r *AssigneeRepo) DeleteByVersion(_ context.Context, _, _ uuid.UUID) error {
	return errors.New("not implemented")
}
