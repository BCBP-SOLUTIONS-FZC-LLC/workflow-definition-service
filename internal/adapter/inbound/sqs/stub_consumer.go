package sqs

import (
	"context"

	"go.uber.org/zap"
)

type StubConsumer struct {
	log *zap.Logger
}

func NewStubConsumer(log *zap.Logger) *StubConsumer {
	return &StubConsumer{log: log}
}

func (s *StubConsumer) Run(ctx context.Context) error {
	s.log.Info("stub: SQS consumer started (no-op — AWS_USE_STUB=true)")
	<-ctx.Done()
	s.log.Info("stub: SQS consumer stopped")
	return nil
}
