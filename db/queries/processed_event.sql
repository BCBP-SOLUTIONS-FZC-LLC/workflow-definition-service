-- name: RecordEventIfNew :one
INSERT INTO processed_event (event_id, consumer, event_type)
VALUES ($1, $2, $3)
ON CONFLICT (event_id, consumer) DO NOTHING
RETURNING event_id;
