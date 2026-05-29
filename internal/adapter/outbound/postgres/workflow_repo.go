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

var _ port.WorkflowRepository = (*WorkflowRepo)(nil)

type WorkflowRepo struct {
	q *db.Queries
}

func NewWorkflowRepo(pool *pgxpool.Pool) *WorkflowRepo {
	return &WorkflowRepo{q: db.New(pool)}
}

func (r *WorkflowRepo) Create(_ context.Context, _ *domain.Workflow) error {
	return errors.New("not implemented")
}

func (r *WorkflowRepo) GetByID(_ context.Context, _, _ uuid.UUID) (*domain.Workflow, error) {
	return nil, errors.New("not implemented")
}

func (r *WorkflowRepo) GetByBusinessKey(_ context.Context, _ uuid.UUID, _ string) (*domain.Workflow, error) {
	return nil, errors.New("not implemented")
}

func (r *WorkflowRepo) List(_ context.Context, _ uuid.UUID, _ port.WorkflowFilter) ([]*domain.Workflow, int64, error) {
	return nil, 0, errors.New("not implemented")
}

func (r *WorkflowRepo) UpdateActiveVersion(_ context.Context, _, _ uuid.UUID, _ *uuid.UUID) error {
	return errors.New("not implemented")
}

func (r *WorkflowRepo) CountByTenant(_ context.Context, _ uuid.UUID) (int64, error) {
	return 0, errors.New("not implemented")
}
