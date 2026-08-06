package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/pkg/events"
	"go.opentelemetry.io/otel/trace"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func buildEnvelope[T any](ctx context.Context, eventType, tenantID, subject, actor string, payload T) (events.Envelope[json.RawMessage], error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return events.Envelope[json.RawMessage]{}, fmt.Errorf("marshal event payload: %w", err)
	}
	opts := []events.EnvelopeOpt{
		events.WithTenantID(tenantID),
		events.WithSchemaVersion("1"),
	}
	if subject != "" {
		opts = append(opts, events.WithSubject(subject))
	}
	if actor != "" {
		opts = append(opts, events.WithActor(actor))
	}
	if sc := trace.SpanFromContext(ctx).SpanContext(); sc.IsValid() {
		opts = append(opts, events.WithTraceID(sc.TraceID().String()))
	}
	return events.NewEnvelope[json.RawMessage](eventType, domain.EventSource, raw, opts...), nil
}
