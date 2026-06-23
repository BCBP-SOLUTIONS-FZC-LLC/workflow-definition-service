DROP TABLE processed_event;

CREATE TABLE processed_event (
    id           UUID PRIMARY KEY,
    tenant_id    UUID NOT NULL,
    source       VARCHAR(255) NOT NULL,
    processed_at TIMESTAMP DEFAULT now()
);

CREATE INDEX idx_processed_event_processed_at ON processed_event(processed_at);

ALTER TABLE processed_event ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_policy ON processed_event
    USING (tenant_id = current_setting('app.tenant_id')::uuid);
