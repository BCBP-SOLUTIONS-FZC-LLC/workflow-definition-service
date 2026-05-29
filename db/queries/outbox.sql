-- name: EnqueueEvent :exec
INSERT INTO outbox (id, tenant_id, topic, payload_json, status)
VALUES ($1, $2, $3, $4, 'PENDING');

-- name: FetchPendingEvents :many
SELECT id, tenant_id, topic, payload_json, status, error_message, retry_count, retry_after, created_at, processed_at
FROM outbox
WHERE status = 'PENDING' AND (retry_after IS NULL OR retry_after <= now())
ORDER BY created_at ASC
LIMIT $1
FOR UPDATE SKIP LOCKED;

-- name: MarkEventSent :exec
UPDATE outbox
SET status = 'SENT', processed_at = now()
WHERE id = $1;

-- name: MarkEventFailed :exec
UPDATE outbox
SET status       = 'FAILED',
    error_message = $2,
    retry_count  = retry_count + 1,
    retry_after  = now() + INTERVAL '30 seconds' * POWER(2, retry_count)
WHERE id = $1;
