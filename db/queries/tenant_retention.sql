-- name: RecordTenantOffboarded :exec
INSERT INTO tenant_retention (tenant_id, offboarded_at, artifacts_purge_after, updated_at)
VALUES ($1, $2, $3, now())
ON CONFLICT (tenant_id) DO NOTHING;

-- name: ListTenantsDueForPurge :many
SELECT tenant_id FROM tenant_retention
WHERE purged_at IS NULL AND artifacts_purge_after <= $1
ORDER BY artifacts_purge_after, tenant_id
LIMIT $2;

-- name: MarkTenantPurged :exec
UPDATE tenant_retention SET purged_at = $2, updated_at = now() WHERE tenant_id = $1;

-- name: PurgeTenantWorkflows :execrows
DELETE FROM workflow
WHERE id IN (SELECT w.id FROM workflow w WHERE w.tenant_id = $1 LIMIT $2);

-- name: PurgeTenantModules :execrows
DELETE FROM workflow_module
WHERE id IN (SELECT m.id FROM workflow_module m WHERE m.tenant_id = $1 AND m.scope = 'tenant' LIMIT $2);

-- name: PurgeTenantStarters :execrows
DELETE FROM workflow_template
WHERE id IN (SELECT t.id FROM workflow_template t WHERE t.tenant_id = $1 AND t.scope = 'tenant' LIMIT $2);
