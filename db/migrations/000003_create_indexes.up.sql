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

CREATE INDEX idx_processed_event_processed_at ON processed_event(processed_at);
