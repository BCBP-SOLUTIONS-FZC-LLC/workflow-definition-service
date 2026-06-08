package postgres

import (
	"context"
	"errors"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgcommon"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres/db"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

var _ port.ProcessedEventRepository = (*ProcessedEventRepo)(nil)

type ProcessedEventRepo struct {
	pool *pgcommon.Pool
}

func NewProcessedEventRepo(pool *pgcommon.Pool) *ProcessedEventRepo {
	return &ProcessedEventRepo{pool: pool}
}

// RecordIfNew inserts the event ID and returns true if new, false if already
// seen (ON CONFLICT DO NOTHING returns no row → pgx.ErrNoRows).
func (r *ProcessedEventRepo) RecordIfNew(
	ctx context.Context,
	eventID, tenantID uuid.UUID,
	source string,
) (bool, error) {
	var isNew bool
	err := exec(ctx, r.pool, func(dbtx db.DBTX) error {
		_, err := db.New(dbtx).RecordEventIfNew(ctx, db.RecordEventIfNewParams{
			ID:       eventID,
			TenantID: tenantID,
			Source:   source,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			isNew = false
			return nil
		}
		if err != nil {
			return mapErr(err)
		}
		isNew = true
		return nil
	})
	return isNew, err
}

func (r *ProcessedEventRepo) PruneOlderThan(ctx context.Context, days int) error {
	return exec(ctx, r.pool, func(dbtx db.DBTX) error {
		return mapErr(db.New(dbtx).PruneEventsOlderThan(ctx, int32(days)))
	})
}
