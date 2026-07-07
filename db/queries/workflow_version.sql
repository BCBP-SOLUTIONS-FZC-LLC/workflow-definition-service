-- name: CreateWorkflowVersion :exec
INSERT INTO workflow_version (
    id, workflow_id, tenant_id, status, bpmn_xml, compiled_plan_json,
    artifact_hash, version_number, published_at, created_by_user_id,
    is_valid, validation_errors_json, module_bpmn_xmls
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13);

-- name: GetWorkflowVersionByID :one
SELECT id, workflow_id, tenant_id, status, bpmn_xml, compiled_plan_json,
       artifact_hash, version_number, published_at, created_by_user_id,
       is_valid, validation_errors_json, created_at, updated_at, record_version,
       module_bpmn_xmls
FROM workflow_version
WHERE tenant_id = $1 AND id = $2;

-- name: GetDraftVersion :one
SELECT id, workflow_id, tenant_id, status, bpmn_xml, compiled_plan_json,
       artifact_hash, version_number, published_at, created_by_user_id,
       is_valid, validation_errors_json, created_at, updated_at, record_version,
       module_bpmn_xmls
FROM workflow_version
WHERE tenant_id = $1 AND workflow_id = $2 AND status = 'DRAFT';

-- name: ListVersionsByWorkflow :many
SELECT id, workflow_id, tenant_id, status, bpmn_xml, compiled_plan_json,
       artifact_hash, version_number, published_at, created_by_user_id,
       is_valid, validation_errors_json, created_at, updated_at, record_version,
       module_bpmn_xmls
FROM workflow_version
WHERE tenant_id = $1 AND workflow_id = $2
ORDER BY created_at DESC
LIMIT $3
OFFSET $4;

-- name: CountVersionsByWorkflow :one
SELECT COUNT(*) FROM workflow_version WHERE tenant_id = $1 AND workflow_id = $2;

-- name: UpdateDraftVersion :execresult
-- updated_at and record_version are bumped by the update_meta_columns trigger.
-- The record_version predicate ($8) is the optimistic-lock guard; 0 rows means
-- not-found, wrong-status, or a concurrent write (disambiguated by the repo).
UPDATE workflow_version
SET bpmn_xml               = $3,
    compiled_plan_json      = $4,
    artifact_hash           = $5,
    is_valid                = $6,
    validation_errors_json  = $7,
    module_bpmn_xmls        = $9
WHERE tenant_id = $1 AND id = $2 AND status = 'DRAFT' AND record_version = $8;

-- name: PublishVersion :execresult
UPDATE workflow_version
SET status          = 'PUBLISHED',
    version_number  = $3,
    compiled_plan_json = $4,
    artifact_hash   = $5,
    published_at    = now(),
    updated_at      = now()
WHERE tenant_id = $1 AND id = $2 AND status = 'DRAFT';

-- name: ArchiveVersion :execresult
UPDATE workflow_version
SET status = 'ARCHIVED', updated_at = now()
WHERE tenant_id = $1 AND id = $2 AND status = 'PUBLISHED';

-- name: DeleteDraftVersion :execresult
DELETE FROM workflow_version
WHERE tenant_id = $1 AND id = $2 AND status = 'DRAFT';

-- name: SetVersionInvalid :execresult
UPDATE workflow_version
SET is_valid               = false,
    validation_errors_json  = $3,
    updated_at              = now()
WHERE tenant_id = $1 AND id = $2;

-- name: NextVersionNumber :one
SELECT COALESCE(MAX(version_number), 0) + 1
FROM workflow_version
WHERE tenant_id = $1 AND workflow_id = $2 AND status != 'DRAFT';
