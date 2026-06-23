CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS update_workflow_meta ON workflow;
DROP TRIGGER IF EXISTS update_workflow_version_meta ON workflow_version;

CREATE TRIGGER update_workflow_updated_at
    BEFORE UPDATE ON workflow
    FOR EACH ROW EXECUTE PROCEDURE update_updated_at_column();

CREATE TRIGGER update_workflow_version_updated_at
    BEFORE UPDATE ON workflow_version
    FOR EACH ROW EXECUTE PROCEDURE update_updated_at_column();

DROP FUNCTION IF EXISTS update_meta_columns();

ALTER TABLE workflow_version DROP COLUMN IF EXISTS record_version;
ALTER TABLE workflow         DROP COLUMN IF EXISTS record_version;
