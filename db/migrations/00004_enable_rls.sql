-- +goose Up

ALTER TABLE workflow ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_policy ON workflow
    USING (tenant_id = current_setting('app.tenant_id')::uuid);

ALTER TABLE workflow_version ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_policy ON workflow_version
    USING (tenant_id = current_setting('app.tenant_id')::uuid);

ALTER TABLE workflow_node_assignee ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_policy ON workflow_node_assignee
    USING (tenant_id = current_setting('app.tenant_id')::uuid);

ALTER TABLE outbox ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_policy ON outbox
    USING (tenant_id = current_setting('app.tenant_id')::uuid);

ALTER TABLE processed_event ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_policy ON processed_event
    USING (tenant_id = current_setting('app.tenant_id')::uuid);

-- +goose Down

DROP POLICY IF EXISTS tenant_isolation_policy ON processed_event;
ALTER TABLE processed_event DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS tenant_isolation_policy ON outbox;
ALTER TABLE outbox DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS tenant_isolation_policy ON workflow_node_assignee;
ALTER TABLE workflow_node_assignee DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS tenant_isolation_policy ON workflow_version;
ALTER TABLE workflow_version DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS tenant_isolation_policy ON workflow;
ALTER TABLE workflow DISABLE ROW LEVEL SECURITY;
