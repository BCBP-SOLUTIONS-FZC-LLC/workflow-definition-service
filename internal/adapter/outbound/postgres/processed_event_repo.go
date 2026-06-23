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

// RecordIfNew inserts the (event_id, consumer) pair and returns true if new,
// false if already seen (ON CONFLICT DO NOTHING returns no row → pgx.ErrNoRows).
// consumer identifies the logical queue/source; eventType is stored for
// observability and may be empty.
func (r *ProcessedEventRepo) RecordIfNew(
	ctx context.Context,
	eventID uuid.UUID,
	consumer, eventType string,
) (bool, error) {
	var isNew bool
	err := exec(ctx, r.pool, func(dbtx db.DBTX) error {
		_, err := db.New(dbtx).RecordEventIfNew(ctx, db.RecordEventIfNewParams{
			EventID:   eventID,
			Consumer:  consumer,
			EventType: toNullableText(eventType),
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
