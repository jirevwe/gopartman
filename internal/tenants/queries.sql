-- name: GetTenants :many
SELECT t.id as tenant_id, pt.id as parent_table_id
FROM partman.tenants t
         JOIN partman.parent_tables pt ON pt.id = t.parent_table_id
WHERE pt.table_name = $1
  AND pt.schema_name = $2
ORDER BY t.id;