-- Align processed_event with the LLD canonical schema: composite PK
-- (event_id, consumer) so the same event can be deduped independently per
-- consumer, plus a nullable event_type for observability. The table is an
-- operational dedup log, not tenant data: drop tenant_id and its RLS policy.
DROP POLICY IF EXISTS tenant_isolation_policy ON processed_event;
ALTER TABLE processed_event DISABLE ROW LEVEL SECURITY;
DROP TABLE processed_event;

CREATE TABLE processed_event (
    event_id     UUID NOT NULL,
    consumer     TEXT NOT NULL,
    event_type   TEXT,
    processed_at TIMESTAMP DEFAULT now(),
    PRIMARY KEY (event_id, consumer)
);

CREATE INDEX idx_processed_event_processed_at ON processed_event(processed_at);
