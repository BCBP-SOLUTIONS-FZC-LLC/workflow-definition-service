package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

var _ port.AssigneeRepository = (*AssigneeRepo)(nil)

type AssigneeRepo struct {
	pool *pgcommon.Pool
}

func NewAssigneeRepo(pool *pgcommon.Pool) *AssigneeRepo {
	return &AssigneeRepo{pool: pool}
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
