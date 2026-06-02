-- +goose Up
CREATE TABLE outbox_events (
    id           UUID        PRIMARY KEY,
    event_type   TEXT        NOT NULL,
    payload      JSONB       NOT NULL,
    tenant_id    TEXT        NOT NULL DEFAULT '',
    trace_id     TEXT        NOT NULL DEFAULT '',
    attempts     INT         NOT NULL DEFAULT 0,
    last_error   TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    scheduled_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at TIMESTAMPTZ
);

CREATE INDEX idx_outbox_events_pending
    ON outbox_events (scheduled_at, id)
    WHERE published_at IS NULL;

CREATE TABLE outbox_dead_letters (
    id         UUID        PRIMARY KEY,
    event_type TEXT        NOT NULL,
    payload    JSONB       NOT NULL,
    tenant_id  TEXT        NOT NULL DEFAULT '',
    trace_id   TEXT        NOT NULL DEFAULT '',
    attempts   INT         NOT NULL,
    last_error TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    failed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_outbox_dead_letters_failed_at
    ON outbox_dead_letters (failed_at DESC);

ALTER TABLE outbox_events ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_policy ON outbox_events
    USING (tenant_id = current_setting('app.tenant_id', true));

ALTER TABLE outbox_dead_letters ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_policy ON outbox_dead_letters
    USING (tenant_id = current_setting('app.tenant_id', true));

-- +goose Down
DROP TABLE IF EXISTS outbox_dead_letters;
DROP TABLE IF EXISTS outbox_events;
