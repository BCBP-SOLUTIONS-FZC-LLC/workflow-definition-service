-- name: RecordEventIfNew :one
INSERT INTO processed_event (id, tenant_id, source)
VALUES ($1, $2, $3)
ON CONFLICT (id) DO NOTHING
RETURNING id;

-- name: PruneEventsOlderThan :exec
DELETE FROM processed_event
WHERE processed_at < now() - ($1::int * INTERVAL '1 day');
