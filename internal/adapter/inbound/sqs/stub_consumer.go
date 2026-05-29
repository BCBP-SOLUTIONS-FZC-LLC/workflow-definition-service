package sqs

import (
	"context"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

type StubConsumer struct {
	log port.Logger
}

func NewStubConsumer(log port.Logger) *StubConsumer {
	return &StubConsumer{log: log}
}

func (s *StubConsumer) Run(ctx context.Context) error {
	s.log.Info("stub: SQS consumer started (no-op — AWS_USE_STUB=true)", nil)
	<-ctx.Done()
	s.log.Info("stub: SQS consumer stopped", nil)
	return nil
}
