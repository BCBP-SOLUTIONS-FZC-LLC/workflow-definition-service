package sns

import (
	"context"

	"go.uber.org/zap"
)

type StubPublisher struct {
	log *zap.Logger
}

func NewStubPublisher(log *zap.Logger) *StubPublisher {
	return &StubPublisher{log: log}
}

func (s *StubPublisher) Publish(ctx context.Context, topic string, payload []byte) error {
	s.log.Info("stub: SNS publish skipped",
		zap.String("topic", topic),
		zap.Int("payload_bytes", len(payload)),
	)
	return nil
}
