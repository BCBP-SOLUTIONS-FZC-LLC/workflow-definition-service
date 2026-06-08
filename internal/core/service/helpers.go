package service

import (
	"encoding/json"
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/pkg/events"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

// buildEnvelope marshals payload to JSON and wraps it in a platform-events
// envelope. Used by all service methods that enqueue outbox events.
func buildEnvelope[T any](eventType, tenantID string, payload T) (events.Envelope[json.RawMessage], error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return events.Envelope[json.RawMessage]{}, fmt.Errorf("marshal event payload: %w", err)
	}
	env := events.NewEnvelope[json.RawMessage](
		eventType,
		domain.EventSource,
		raw,
		events.WithTenantID(tenantID),
	)
	return env, nil
}
