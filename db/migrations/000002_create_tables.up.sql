CREATE TABLE workflow (
    id                  UUID PRIMARY KEY,
    tenant_id           UUID NOT NULL,
    created_by_user_id  UUID NOT NULL,
    business_key        VARCHAR(255) NOT NULL,
    name                VARCHAR(255) NOT NULL,
    description         TEXT,
    active_version_id   UUID,
    created_at          TIMESTAMP DEFAULT now(),
    updated_at          TIMESTAMP DEFAULT now(),
    record_version      BIGINT NOT NULL DEFAULT 1,
    UNIQUE (tenant_id, business_key)
);

CREATE TABLE workflow_version (
    id                      UUID PRIMARY KEY,
    workflow_id             UUID REFERENCES workflow(id) ON DELETE CASCADE,
    tenant_id               UUID NOT NULL,
    status                  workflow_version_status NOT NULL,
    bpmn_xml                TEXT NOT NULL,
    compiled_plan_json      JSONB,
    artifact_hash           TEXT,
    version_number          INT,
    published_at            TIMESTAMP,
    created_by_user_id      UUID NOT NULL,
    is_valid                BOOLEAN NOT NULL DEFAULT true,
    validation_errors_json  JSONB,
    created_at              TIMESTAMP DEFAULT now(),
    updated_at              TIMESTAMP DEFAULT now(),
    record_version          BIGINT NOT NULL DEFAULT 1,
    module_bpmn_xmls        TEXT[] NOT NULL DEFAULT '{}',
    CONSTRAINT chk_version_number_on_publish
        CHECK (status = 'DRAFT' OR version_number IS NOT NULL)
);

ALTER TABLE workflow
    ADD CONSTRAINT fk_active_version
    FOREIGN KEY (active_version_id) REFERENCES workflow_version(id)
    ON DELETE SET NULL DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE workflow_node_assignee (
    id                  UUID PRIMARY KEY,
    tenant_id           UUID NOT NULL,
    workflow_version_id UUID REFERENCES workflow_version(id) ON DELETE CASCADE,
    node_key            VARCHAR(255) NOT NULL,
    user_id             UUID NOT NULL,
    department_id       UUID NOT NULL,
    role                VARCHAR(64) NOT NULL,
    created_at          TIMESTAMP DEFAULT now()
);

-- Operational dedup log, not tenant data — composite PK so the same event can
-- be deduped independently per consumer; no RLS.
CREATE TABLE processed_event (
    event_id     UUID NOT NULL,
    consumer     TEXT NOT NULL,
    event_type   TEXT,
    processed_at TIMESTAMP DEFAULT now(),
    PRIMARY KEY (event_id, consumer)
);

-- Operational audit log (same class as processed_event, outbox_events); no
-- RLS. Populated by DB-level triggers on RLS violations, not application code.
-- Cleaned by the external periodic job per LLD §7.1.2 (GAP-11).
CREATE TABLE rls_violation_log (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  UUID,
    user_id    UUID,
    table_name TEXT        NOT NULL,
    operation  TEXT        NOT NULL,
    client_ip  INET,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE OR REPLACE FUNCTION update_meta_columns()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    NEW.record_version = OLD.record_version + 1;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER update_workflow_meta
    BEFORE UPDATE ON workflow
    FOR EACH ROW
    WHEN (OLD.* IS DISTINCT FROM NEW.*)
    EXECUTE PROCEDURE update_meta_columns();

CREATE TRIGGER update_workflow_version_meta
    BEFORE UPDATE ON workflow_version
    FOR EACH ROW
    WHEN (OLD.* IS DISTINCT FROM NEW.*)
    EXECUTE PROCEDURE update_meta_columns();
