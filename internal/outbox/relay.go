package outbox

import (
	"context"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

type Relay struct {
	repo      port.OutboxRepository
	publisher port.EventPublisher
	interval  time.Duration
	batchSize int
	log       port.Logger
}

func NewRelay(
	repo port.OutboxRepository,
	publisher port.EventPublisher,
	interval time.Duration,
	batchSize int,
	log port.Logger,
) *Relay {
	return &Relay{
		repo:      repo,
		publisher: publisher,
		interval:  interval,
		batchSize: batchSize,
		log:       log,
	}
}

func (r *Relay) Run(ctx context.Context) error {
	r.log.Info("stub: outbox relay started (no-op)", nil)
	<-ctx.Done()
	r.log.Info("stub: outbox relay stopped", nil)
	return nil
}
