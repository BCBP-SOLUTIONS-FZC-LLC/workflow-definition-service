package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

var _ port.WorkflowVersionRepository = (*WorkflowVersionRepo)(nil)

type WorkflowVersionRepo struct {
	pool *pgcommon.Pool
}

func NewWorkflowVersionRepo(pool *pgcommon.Pool) *WorkflowVersionRepo {
	return &WorkflowVersionRepo{pool: pool}
}

func (r *WorkflowVersionRepo) Create(_ context.Context, _ *domain.WorkflowVersion) error {
	return errors.New("not implemented")
}

func (r *WorkflowVersionRepo) GetByID(_ context.Context, _, _ uuid.UUID) (*domain.WorkflowVersion, error) {
	return nil, errors.New("not implemented")
}

func (r *WorkflowVersionRepo) GetDraft(_ context.Context, _, _ uuid.UUID) (*domain.WorkflowVersion, error) {
	return nil, errors.New("not implemented")
}

func (r *WorkflowVersionRepo) ListByWorkflow(_ context.Context, _, _ uuid.UUID, _, _ int) ([]*domain.WorkflowVersion, int64, error) {
	return nil, 0, errors.New("not implemented")
}

func (r *WorkflowVersionRepo) UpdateDraft(_ context.Context, _ *domain.WorkflowVersion) error {
	return errors.New("not implemented")
}

func (r *WorkflowVersionRepo) Publish(_ context.Context, _, _ uuid.UUID, _ int32, _, _ string) error {
	return errors.New("not implemented")
}

func (r *WorkflowVersionRepo) Archive(_ context.Context, _, _ uuid.UUID) error {
	return errors.New("not implemented")
}

func (r *WorkflowVersionRepo) DeleteDraft(_ context.Context, _, _ uuid.UUID) error {
	return errors.New("not implemented")
}

func (r *WorkflowVersionRepo) SetInvalid(_ context.Context, _, _ uuid.UUID, _ string) error {
	return errors.New("not implemented")
}

func (r *WorkflowVersionRepo) NextVersionNumber(_ context.Context, _, _ uuid.UUID) (int32, error) {
	return 0, errors.New("not implemented")
}
