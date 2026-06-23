-- Optimistic-lock token on workflow and workflow_version. A BEFORE UPDATE
-- trigger bumps record_version (and updated_at) on every real change.
ALTER TABLE workflow         ADD COLUMN record_version BIGINT NOT NULL DEFAULT 1;
ALTER TABLE workflow_version ADD COLUMN record_version BIGINT NOT NULL DEFAULT 1;

CREATE OR REPLACE FUNCTION update_meta_columns()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    NEW.record_version = OLD.record_version + 1;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Recreate the triggers using update_meta_columns, guarded so a no-op UPDATE
-- (no column actually changed) neither bumps record_version nor updated_at.
DROP TRIGGER IF EXISTS update_workflow_updated_at ON workflow;
DROP TRIGGER IF EXISTS update_workflow_version_updated_at ON workflow_version;

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

DROP FUNCTION IF EXISTS update_updated_at_column();
