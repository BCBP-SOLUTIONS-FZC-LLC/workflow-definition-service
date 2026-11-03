package service

import (
	"context"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/gincommon"
	"github.com/google/uuid"
)

func stampAudit(ctx context.Context, fields map[string]any) map[string]any {
	fields["trace_id"] = gincommon.SpanTraceID(ctx)
	return fields
}

func auditFields(ctx context.Context, tenantID, userID uuid.UUID, extra map[string]any) map[string]any {
	extra["tenant_id"] = tenantID.String()
	extra["user_id"] = userID.String()
	return stampAudit(ctx, extra)
}
