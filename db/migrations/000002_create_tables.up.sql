CREATE TABLE workflow (
    id                  UUID PRIMARY KEY,
    tenant_id           UUID NOT NULL,
    created_by_user_id  UUID NOT NULL,
    template_key        VARCHAR(255) NOT NULL,
    name                VARCHAR(255) NOT NULL,
    description         TEXT,
    active_version_id   UUID,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    record_version      BIGINT NOT NULL DEFAULT 1,
    UNIQUE (tenant_id, template_key),
    UNIQUE (id, tenant_id),
    CONSTRAINT chk_workflow_record_version CHECK (record_version > 0)
);

CREATE TABLE workflow_version (
    id                      UUID PRIMARY KEY,
    workflow_id             UUID NOT NULL,
    tenant_id               UUID NOT NULL,
    status                  workflow_version_status NOT NULL,
    bpmn_xml                TEXT NOT NULL,
    compiled_plan_json      JSONB,
    artifact_hash           TEXT,
    version_number          INT,
    published_at            TIMESTAMPTZ,
    created_by_user_id      UUID NOT NULL,
    is_valid                BOOLEAN NOT NULL DEFAULT true,
    validation_errors_json  JSONB,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    record_version          BIGINT NOT NULL DEFAULT 1,
    module_refs             JSONB NOT NULL DEFAULT '[]',
    CONSTRAINT chk_version_number_on_publish
        CHECK (status = 'DRAFT' OR version_number IS NOT NULL),
    CONSTRAINT chk_workflow_version_record_version CHECK (record_version > 0),
    UNIQUE (id, tenant_id),
    FOREIGN KEY (workflow_id, tenant_id) REFERENCES workflow(id, tenant_id) ON DELETE CASCADE
);

ALTER TABLE workflow
    ADD CONSTRAINT fk_active_version
    FOREIGN KEY (active_version_id, tenant_id) REFERENCES workflow_version(id, tenant_id)
    ON DELETE SET NULL (active_version_id) DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE workflow_node_assignee (
    id                  UUID PRIMARY KEY,
    tenant_id           UUID NOT NULL,
    workflow_version_id UUID NOT NULL,
    node_key            VARCHAR(255) NOT NULL,
    user_id             UUID NOT NULL,
    department_id       UUID NOT NULL,
    role                VARCHAR(64) NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (workflow_version_id, tenant_id) REFERENCES workflow_version(id, tenant_id) ON DELETE CASCADE
);

CREATE TABLE workflow_module (
    id                  UUID PRIMARY KEY,
    tenant_id           UUID,
    scope               catalog_scope NOT NULL,
    name                VARCHAR(255) NOT NULL,
    description         TEXT,
    process_id          VARCHAR(255) NOT NULL,
    active_version_id   UUID,
    created_by_user_id  UUID,
    record_version      BIGINT NOT NULL DEFAULT 1,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT chk_module_scope_tenant CHECK (
        (scope = 'global' AND tenant_id IS NULL) OR
        (scope = 'tenant' AND tenant_id IS NOT NULL)
    ),
    CONSTRAINT chk_workflow_module_record_version CHECK (record_version > 0),
    UNIQUE (id, tenant_id),
    UNIQUE (id, scope)
);

CREATE TABLE workflow_module_version (
    id                      UUID PRIMARY KEY,
    module_id               UUID NOT NULL,
    tenant_id               UUID,
    scope                   catalog_scope NOT NULL,
    status                  workflow_version_status NOT NULL,
    bpmn_xml                TEXT NOT NULL,
    process_id              VARCHAR(255) NOT NULL,
    version_number          INT,
    is_valid                BOOLEAN NOT NULL DEFAULT true,
    validation_errors_json  JSONB,
    published_at            TIMESTAMPTZ,
    created_by_user_id      UUID,
    record_version          BIGINT NOT NULL DEFAULT 1,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT chk_module_version_number_on_publish
        CHECK (status = 'DRAFT' OR version_number IS NOT NULL),
    CONSTRAINT chk_module_version_scope_tenant CHECK (
        (scope = 'global' AND tenant_id IS NULL) OR
        (scope = 'tenant' AND tenant_id IS NOT NULL)
    ),
    CONSTRAINT chk_workflow_module_version_record_version CHECK (record_version > 0),
    UNIQUE (id, tenant_id)
);

ALTER TABLE workflow_module_version
    ADD CONSTRAINT fk_module_version_module
    FOREIGN KEY (module_id, tenant_id) REFERENCES workflow_module(id, tenant_id) ON DELETE CASCADE;

ALTER TABLE workflow_module_version
    ADD CONSTRAINT fk_module_version_module_scope
    FOREIGN KEY (module_id, scope) REFERENCES workflow_module(id, scope) ON DELETE CASCADE;

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
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT chk_template_scope_tenant CHECK (
        (scope = 'global' AND tenant_id IS NULL) OR
        (scope = 'tenant' AND tenant_id IS NOT NULL)
    ),
    CONSTRAINT chk_workflow_template_record_version CHECK (record_version > 0)
);

-- Org-owned internal service registry, not tenant data — no RLS.
CREATE TABLE connector_rest_alias (
    alias         TEXT PRIMARY KEY,
    method        TEXT NOT NULL,
    base_url      TEXT NOT NULL,
    path_template TEXT NOT NULL,
    timeout_ms    INTEGER NOT NULL DEFAULT 5000
);

-- Sampled RLS violations written by rls_check_tenant() and
-- rls_check_tenant_or_global() (000004). RLS stays disabled on this table: the
-- functions are SECURITY DEFINER and write here, so a policy would recurse.
CREATE TABLE rls_violation_log (
    id               BIGSERIAL   PRIMARY KEY,
    occurred_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    session_role     TEXT        NOT NULL DEFAULT session_user,
    violation_type   TEXT        NOT NULL CHECK (violation_type IN ('missing_or_invalid_guc', 'cross_tenant_access')),
    app_tenant_id    UUID,
    row_tenant_id    UUID        NOT NULL,
    table_name       TEXT        NOT NULL,
    client_addr      INET        DEFAULT inet_client_addr(),
    application_name TEXT        DEFAULT current_setting('application_name', true),
    query_text       TEXT
);

REVOKE ALL ON rls_violation_log FROM PUBLIC;

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
