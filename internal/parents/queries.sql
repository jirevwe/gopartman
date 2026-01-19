-- name: UpsertParentTable :one
INSERT INTO partman.parent_tables
(id, table_name, schema_name,
 tenant_column, partition_by,
 partition_type, partition_interval,
 partition_count, retention_period)
VALUES (@id,
        @table_name,
        @schema_name,
        @tenant_column,
        @partition_by,
        @partition_type,
        @partition_interval,
        @partition_count,
        @retention_period)
ON CONFLICT DO NOTHING
RETURNING *;