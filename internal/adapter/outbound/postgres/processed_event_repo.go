package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

var _ port.ProcessedEventRepository = (*ProcessedEventRepo)(nil)

type ProcessedEventRepo struct {
	pool *pgcommon.Pool
}

func NewProcessedEventRepo(pool *pgcommon.Pool) *ProcessedEventRepo {
	return &ProcessedEventRepo{pool: pool}
}

func (r *ProcessedEventRepo) RecordIfNew(_ context.Context, _, _ uuid.UUID, _ string) (bool, error) {
	return false, errors.New("not implemented")
}

func (r *ProcessedEventRepo) PruneOlderThan(_ context.Context, _ int) error {
	return errors.New("not implemented")
}
