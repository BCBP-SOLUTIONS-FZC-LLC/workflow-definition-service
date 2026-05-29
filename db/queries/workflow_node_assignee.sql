-- name: InsertAssignee :batchexec
INSERT INTO workflow_node_assignee (id, tenant_id, workflow_version_id, node_key, user_id, department_id, role)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: ListAssigneesByUser :many
SELECT id, tenant_id, workflow_version_id, node_key, user_id, department_id, role, created_at
FROM workflow_node_assignee
WHERE tenant_id = $1 AND user_id = $2;

-- name: DeleteAssigneesByVersion :exec
DELETE FROM workflow_node_assignee
WHERE tenant_id = $1 AND workflow_version_id = $2;
