-- name: UpsertPartition :exec
INSERT INTO partman.partitions
(id,
 name,
 parent_table_id,
 tenant_id,
 partition_by,
 partition_type,
 partition_bounds_from,
 partition_bounds_to)
VALUES (@id,
        @name,
        @parent_table_id,
        @tenant_id,
        @partition_by,
        @partition_type,
        @partition_bounds_from,
        @partition_bounds_to)
ON CONFLICT DO NOTHING
RETURNING *;