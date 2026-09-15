-- name: UpsertRestAlias :exec
INSERT INTO connector_rest_alias (alias, method, base_url, path_template, timeout_ms)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (alias) DO UPDATE
SET method = $2, base_url = $3, path_template = $4, timeout_ms = $5;

-- name: ListRestAliases :many
SELECT alias, method, base_url, path_template, timeout_ms
FROM connector_rest_alias
ORDER BY alias;

-- name: DeleteRestAlias :execrows
DELETE FROM connector_rest_alias WHERE alias = $1;
