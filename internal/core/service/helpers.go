package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/pkg/events"
	"go.opentelemetry.io/otel/trace"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

func buildEnvelope[T any](ctx context.Context, codec port.GlueCodec, eventType, tenantID string, payload T) (events.Envelope[json.RawMessage], error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return events.Envelope[json.RawMessage]{}, fmt.Errorf("marshal event payload: %w", err)
	}
	encoded, err := codec.Encode(ctx, eventType, raw)
	if err != nil {
		return events.Envelope[json.RawMessage]{}, fmt.Errorf("glue encode event payload: %w", err)
	}
	opts := []events.EnvelopeOpt{
		events.WithTenantID(tenantID),
		events.WithSchemaVersion("1"),
	}
	if sc := trace.SpanFromContext(ctx).SpanContext(); sc.IsValid() {
		opts = append(opts, events.WithTraceID(sc.TraceID().String()))
	}
	return events.NewEnvelope[json.RawMessage](eventType, domain.EventSource, encoded, opts...), nil
}

type noopGlueCodec struct{}

func (noopGlueCodec) Encode(ctx context.Context, schemaName string, payload []byte) ([]byte, error) {
	return payload, nil
}
