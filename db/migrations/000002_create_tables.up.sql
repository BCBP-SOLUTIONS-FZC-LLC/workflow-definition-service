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

CREATE TABLE workflow_module (
    id                  UUID PRIMARY KEY,
    tenant_id           UUID,
    scope               catalog_scope NOT NULL,
    name                VARCHAR(255) NOT NULL,
    description         TEXT,
    active_version_id   UUID,
    created_by_user_id  UUID,
    record_version      BIGINT NOT NULL DEFAULT 1,
    created_at          TIMESTAMP DEFAULT now(),
    updated_at          TIMESTAMP DEFAULT now(),

    CONSTRAINT chk_module_scope_tenant CHECK (
        (scope = 'global' AND tenant_id IS NULL) OR
        (scope = 'tenant' AND tenant_id IS NOT NULL)
    )
);

CREATE TABLE workflow_module_version (
    id                      UUID PRIMARY KEY,
    module_id               UUID REFERENCES workflow_module(id) ON DELETE CASCADE,
    tenant_id               UUID,
    scope                   catalog_scope NOT NULL,
    status                  workflow_version_status NOT NULL,
    bpmn_xml                TEXT NOT NULL,
    process_id              VARCHAR(255) NOT NULL,
    version_number          INT,
    is_valid                BOOLEAN NOT NULL DEFAULT true,
    validation_errors_json  JSONB,
    published_at            TIMESTAMP,
    created_by_user_id      UUID,
    record_version          BIGINT NOT NULL DEFAULT 1,
    created_at              TIMESTAMP DEFAULT now(),
    updated_at              TIMESTAMP DEFAULT now(),

    CONSTRAINT chk_module_version_number_on_publish
        CHECK (status = 'DRAFT' OR version_number IS NOT NULL),
    CONSTRAINT chk_module_version_scope_tenant CHECK (
        (scope = 'global' AND tenant_id IS NULL) OR
        (scope = 'tenant' AND tenant_id IS NOT NULL)
    )
);

ALTER TABLE workflow_module
    ADD CONSTRAINT fk_module_active_version
    FOREIGN KEY (active_version_id) REFERENCES workflow_module_version(id)
    ON DELETE SET NULL DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE workflow_template (
    id                          UUID PRIMARY KEY,
    tenant_id                   UUID,
    scope                       catalog_scope NOT NULL,
    name                        VARCHAR(255) NOT NULL,
    description                 TEXT,
    category                    VARCHAR(64),
    bpmn_xml                    TEXT NOT NULL,
    source_workflow_version_id  UUID,
    created_by_user_id          UUID,
    record_version              BIGINT NOT NULL DEFAULT 1,
    created_at                  TIMESTAMP DEFAULT now(),
    updated_at                  TIMESTAMP DEFAULT now(),

    CONSTRAINT chk_template_scope_tenant CHECK (
        (scope = 'global' AND tenant_id IS NULL) OR
        (scope = 'tenant' AND tenant_id IS NOT NULL)
    )
);

-- Org-owned internal service registry, not tenant data — no RLS.
CREATE TABLE connector_rest_alias (
    alias         TEXT PRIMARY KEY,
    method        TEXT NOT NULL,
    base_url      TEXT NOT NULL,
    path_template TEXT NOT NULL,
    timeout_ms    INTEGER NOT NULL DEFAULT 5000
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

CREATE TRIGGER update_workflow_module_meta
    BEFORE UPDATE ON workflow_module
    FOR EACH ROW
    WHEN (OLD.* IS DISTINCT FROM NEW.*)
    EXECUTE PROCEDURE update_meta_columns();

CREATE TRIGGER update_workflow_module_version_meta
    BEFORE UPDATE ON workflow_module_version
    FOR EACH ROW
    WHEN (OLD.* IS DISTINCT FROM NEW.*)
    EXECUTE PROCEDURE update_meta_columns();

CREATE TRIGGER update_workflow_template_meta
    BEFORE UPDATE ON workflow_template
    FOR EACH ROW
    WHEN (OLD.* IS DISTINCT FROM NEW.*)
    EXECUTE PROCEDURE update_meta_columns();
