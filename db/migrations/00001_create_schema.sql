-- +goose Up

CREATE TYPE workflow_version_status AS ENUM ('DRAFT', 'PUBLISHED', 'ARCHIVED');

-- +goose Down

DROP TYPE IF EXISTS workflow_version_status;
