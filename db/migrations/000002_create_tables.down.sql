DROP TRIGGER IF EXISTS update_workflow_version_updated_at ON workflow_version;
DROP TRIGGER IF EXISTS update_workflow_updated_at ON workflow;
DROP FUNCTION IF EXISTS update_updated_at_column();
DROP TABLE IF EXISTS processed_event;
DROP TABLE IF EXISTS workflow_node_assignee;
ALTER TABLE workflow DROP CONSTRAINT IF EXISTS fk_active_version;
DROP TABLE IF EXISTS workflow_version;
DROP TABLE IF EXISTS workflow;
