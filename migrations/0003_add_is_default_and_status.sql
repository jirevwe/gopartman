ALTER TABLE partman.partitions
    ADD COLUMN IF NOT EXISTS is_default BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS status     VARCHAR NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'detached', 'dropped'));

CREATE UNIQUE INDEX IF NOT EXISTS partman_default_partition_unique
    ON partman.partitions (parent_table_id, tenant_id)
    NULLS NOT DISTINCT
    WHERE is_default = true;
