-- name: UpsertPartition :exec
INSERT INTO partman.partitions
(id,
 name,
 parent_table_id,
 tenant_id,
 partition_by,
 partition_type,
 partition_bounds_from,
 partition_bounds_to,
 is_default)
VALUES (@id,
        @name,
        @parent_table_id,
        @tenant_id,
        @partition_by,
        @partition_type,
        @partition_bounds_from,
        @partition_bounds_to,
        @is_default)
ON CONFLICT DO NOTHING
RETURNING *;

-- name: ListPartitionsForParent :many
SELECT *
FROM partman.partitions
WHERE parent_table_id = @parent_table_id
ORDER BY partition_bounds_from;

-- name: MarkPartitionDetached :exec
UPDATE partman.partitions
SET status     = 'detached',
    updated_at = CURRENT_TIMESTAMP
WHERE id = @id;

-- name: MarkPartitionDropped :exec
UPDATE partman.partitions
SET status     = 'dropped',
    updated_at = CURRENT_TIMESTAMP
WHERE id = @id;

-- name: GetDefaultPartition :one
SELECT *
FROM partman.partitions
WHERE parent_table_id = @parent_table_id
  AND tenant_id IS NOT DISTINCT FROM @tenant_id
  AND is_default = true;

-- name: ListExpiredPartitions :many
SELECT *
FROM partman.partitions
WHERE parent_table_id = @parent_table_id
  AND partition_bounds_to <= @cutoff
  AND is_default = false
  AND status = 'active'
ORDER BY partition_bounds_from;

-- name: FindActivePartitionByBounds :one
SELECT *
FROM partman.partitions
WHERE parent_table_id = @parent_table_id
  AND tenant_id IS NOT DISTINCT FROM @tenant_id
  AND is_default = false
  AND status = 'active'
  AND partition_bounds_from = @bounds_from
  AND partition_bounds_to = @bounds_to;
