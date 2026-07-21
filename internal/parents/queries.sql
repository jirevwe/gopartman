-- name: UpsertParentTable :one
INSERT INTO partman.parent_tables
(id, table_name, schema_name,
 tenant_column, partition_by,
 partition_type, partition_interval,
 retention_period, retention_keep_table,
 retention_schema, automatic_maintenance,
 premake)
VALUES (@id,
        @table_name,
        @schema_name,
        @tenant_column,
        @partition_by,
        @partition_type,
        @partition_interval,
        @retention_period,
        @retention_keep_table,
        @retention_schema,
        @automatic_maintenance,
        @premake)
ON CONFLICT DO NOTHING
RETURNING *;

-- name: GetParentTable :one
SELECT *
FROM partman.parent_tables
WHERE schema_name = @schema_name
  AND table_name = @table_name;

-- name: ListParentTables :many
SELECT *
FROM partman.parent_tables
ORDER BY schema_name, table_name;

-- name: UpdateAutomaticMaintenance :exec
UPDATE partman.parent_tables
SET automatic_maintenance = @automatic_maintenance,
    updated_at            = CURRENT_TIMESTAMP
WHERE id = @id;
