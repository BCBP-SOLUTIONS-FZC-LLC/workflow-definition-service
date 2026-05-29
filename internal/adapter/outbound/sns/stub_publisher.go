package sns

import (
	"context"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

type StubPublisher struct {
	log port.Logger
}

func NewStubPublisher(log port.Logger) *StubPublisher {
	return &StubPublisher{log: log}
}

func (s *StubPublisher) Publish(_ context.Context, topic string, payload []byte) error {
	s.log.Info("stub: SNS publish skipped", map[string]any{
		"topic":         topic,
		"payload_bytes": len(payload),
	})
	return nil
}
