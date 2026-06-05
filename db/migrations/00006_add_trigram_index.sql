-- +goose Up
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS idx_workflow_name_trgm ON workflow USING gin (name gin_trgm_ops);

-- +goose Down
DROP INDEX IF EXISTS idx_workflow_name_trgm;
