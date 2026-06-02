-- +goose Up

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
    department_id       VARCHAR(255) NOT NULL,
    role                VARCHAR(64) NOT NULL,
    created_at          TIMESTAMP DEFAULT now()
);

CREATE TABLE processed_event (
    id           UUID PRIMARY KEY,
    tenant_id    UUID NOT NULL,
    source       VARCHAR(255) NOT NULL,
    processed_at TIMESTAMP DEFAULT now()
);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER update_workflow_updated_at
    BEFORE UPDATE ON workflow
    FOR EACH ROW EXECUTE PROCEDURE update_updated_at_column();

CREATE TRIGGER update_workflow_version_updated_at
    BEFORE UPDATE ON workflow_version
    FOR EACH ROW EXECUTE PROCEDURE update_updated_at_column();

-- +goose Down

DROP TRIGGER IF EXISTS update_workflow_version_updated_at ON workflow_version;
DROP TRIGGER IF EXISTS update_workflow_updated_at ON workflow;
DROP FUNCTION IF EXISTS update_updated_at_column();
DROP TABLE IF EXISTS processed_event;
DROP TABLE IF EXISTS workflow_node_assignee;
ALTER TABLE workflow DROP CONSTRAINT IF EXISTS fk_active_version;
DROP TABLE IF EXISTS workflow_version;
DROP TABLE IF EXISTS workflow;
