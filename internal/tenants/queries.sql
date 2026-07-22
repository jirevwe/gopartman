-- name: GetTenants :many
SELECT t.id as tenant_id, pt.id as parent_table_id
FROM partman.tenants t
         JOIN partman.parent_tables pt ON pt.id = t.parent_table_id
WHERE pt.table_name = @table_name
  AND pt.schema_name = @schema_name
ORDER BY t.id;

-- name: UpsertTenant :execrows
INSERT INTO partman.tenants (id, parent_table_id)
VALUES (@id, @parent_table_id)
ON CONFLICT DO NOTHING;

-- name: DeleteTenant :execrows
DELETE
FROM partman.tenants
WHERE parent_table_id = @parent_table_id
  AND id = @id;

-- name: ListTenantsForParent :many
SELECT id, parent_table_id, created_at
FROM partman.tenants
WHERE parent_table_id = @parent_table_id
ORDER BY id;
