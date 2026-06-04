-- name: EnqueueEvent :exec
INSERT INTO outbox_events (id, event_type, payload, tenant_id, trace_id, created_at, scheduled_at)
VALUES ($1, $2, $3, $4, $5, NOW(), NOW());
