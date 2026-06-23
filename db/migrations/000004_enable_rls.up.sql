ALTER TABLE workflow ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_policy ON workflow
    USING (tenant_id = current_setting('app.tenant_id')::uuid);

ALTER TABLE workflow_version ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_policy ON workflow_version
    USING (tenant_id = current_setting('app.tenant_id')::uuid);

ALTER TABLE workflow_node_assignee ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_policy ON workflow_node_assignee
    USING (tenant_id = current_setting('app.tenant_id')::uuid);

ALTER TABLE processed_event ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_policy ON processed_event
    USING (tenant_id = current_setting('app.tenant_id')::uuid);
