package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

var _ port.WorkflowRepository = (*WorkflowRepo)(nil)

type WorkflowRepo struct {
	pool *pgcommon.Pool
}

func NewWorkflowRepo(pool *pgcommon.Pool) *WorkflowRepo {
	return &WorkflowRepo{pool: pool}
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
