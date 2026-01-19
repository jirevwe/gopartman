-- name: GetTenants :many
SELECT t.id as tenant_id, pt.id as parent_table_id
FROM partman.tenants t
         JOIN partman.parent_tables pt ON pt.id = t.parent_table_id
WHERE pt.table_name = @table_name
  AND pt.schema_name = @schema_name
ORDER BY t.id;