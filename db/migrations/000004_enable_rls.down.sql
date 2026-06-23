DROP POLICY IF EXISTS tenant_isolation_policy ON processed_event;
ALTER TABLE processed_event DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS tenant_isolation_policy ON workflow_node_assignee;
ALTER TABLE workflow_node_assignee DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS tenant_isolation_policy ON workflow_version;
ALTER TABLE workflow_version DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS tenant_isolation_policy ON workflow;
ALTER TABLE workflow DISABLE ROW LEVEL SECURITY;
