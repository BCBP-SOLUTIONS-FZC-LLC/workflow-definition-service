-- name: CreateModuleVersion :exec
INSERT INTO workflow_module_version (
    id, module_id, tenant_id, scope, status, bpmn_xml, process_id,
    version_number, is_valid, validation_errors_json, created_by_user_id
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11);

-- name: GetModuleVersionByID :one
SELECT id, module_id, tenant_id, scope, status, bpmn_xml, process_id,
       version_number, is_valid, validation_errors_json, published_at,
       created_by_user_id, created_at, updated_at, record_version
FROM workflow_module_version
WHERE id = $1 AND (scope = 'global' OR tenant_id = $2);

-- name: ListModuleVersionsByModule :many
SELECT id, module_id, tenant_id, scope, status, bpmn_xml, process_id,
       version_number, is_valid, validation_errors_json, published_at,
       created_by_user_id, created_at, updated_at, record_version
FROM workflow_module_version
WHERE module_id = $1 AND (scope = 'global' OR tenant_id = $2)
ORDER BY created_at DESC
LIMIT $3
OFFSET $4;

-- name: CountModuleVersionsByModule :one
SELECT COUNT(*) FROM workflow_module_version
WHERE module_id = $1 AND (scope = 'global' OR tenant_id = $2);

-- name: PublishModuleVersion :execresult
UPDATE workflow_module_version
SET status = 'PUBLISHED', version_number = $2, published_at = now()
WHERE id = $1 AND status = 'DRAFT';

-- name: ArchiveModuleVersion :execresult
UPDATE workflow_module_version SET status = 'ARCHIVED' WHERE id = $1 AND status = 'PUBLISHED';

-- name: NextModuleVersionNumber :one
SELECT COALESCE(MAX(version_number), 0) + 1
FROM workflow_module_version WHERE module_id = $1 AND status != 'DRAFT';
