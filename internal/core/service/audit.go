package service

import (
	"context"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
)

func stampAudit(ctx context.Context, fields map[string]any) map[string]any {
	fields["trace_id"] = trace.SpanFromContext(ctx).SpanContext().TraceID().String()
	return fields
}

func auditFields(ctx context.Context, tenantID, userID uuid.UUID, extra map[string]any) map[string]any {
	extra["tenant_id"] = tenantID.String()
	extra["user_id"] = userID.String()
	return stampAudit(ctx, extra)
}
