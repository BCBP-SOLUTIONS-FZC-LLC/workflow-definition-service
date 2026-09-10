CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE INDEX idx_workflow_tenant_id ON workflow(tenant_id);
CREATE INDEX idx_workflow_active_version ON workflow(active_version_id) WHERE active_version_id IS NOT NULL;
CREATE INDEX idx_workflow_name_trgm ON workflow USING gin (name gin_trgm_ops);

CREATE INDEX idx_wv_workflow_id ON workflow_version(workflow_id);
CREATE INDEX idx_wv_tenant_status ON workflow_version(tenant_id, status);
CREATE UNIQUE INDEX idx_wv_single_draft ON workflow_version(workflow_id) WHERE status = 'DRAFT';
CREATE INDEX idx_wv_artifact_hash ON workflow_version(workflow_id, artifact_hash) WHERE status = 'PUBLISHED';
CREATE UNIQUE INDEX uq_workflow_version_published ON workflow_version(workflow_id, version_number)
    WHERE version_number IS NOT NULL;

CREATE INDEX idx_wnas_user_tenant ON workflow_node_assignee(user_id, tenant_id);
CREATE INDEX idx_wnas_version ON workflow_node_assignee(workflow_version_id);

CREATE INDEX idx_module_name_trgm ON workflow_module USING gin (name gin_trgm_ops);
CREATE INDEX idx_module_scope_tenant ON workflow_module(scope, tenant_id);
CREATE INDEX idx_module_active_version ON workflow_module(active_version_id) WHERE active_version_id IS NOT NULL;

CREATE INDEX idx_mv_module_id ON workflow_module_version(module_id);
CREATE UNIQUE INDEX idx_mv_single_draft ON workflow_module_version(module_id) WHERE status = 'DRAFT';
CREATE UNIQUE INDEX uq_module_version_published ON workflow_module_version(module_id, version_number)
    WHERE version_number IS NOT NULL;

CREATE INDEX idx_template_name_trgm ON workflow_template USING gin (name gin_trgm_ops);
CREATE INDEX idx_template_scope_tenant_category ON workflow_template(scope, tenant_id, category);
