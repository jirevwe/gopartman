create schema if not exists partman;

CREATE TABLE IF NOT EXISTS partman.parent_tables
(
    id                    VARCHAR PRIMARY KEY,
    schema_name           VARCHAR  NOT NULL,
    table_name            VARCHAR  NOT NULL,
    tenant_column         VARCHAR,
    partition_by          VARCHAR  NOT NULL,
    partition_type        VARCHAR  NOT NULL,
    partition_interval    VARCHAR  NOT NULL,
    retention_period      INTERVAL NOT NULL,
    retention_keep_table  BOOLEAN  NOT NULL DEFAULT false,
    retention_schema      VARCHAR,
    automatic_maintenance BOOLEAN  NOT NULL DEFAULT true,
    premake               INT      NOT NULL DEFAULT 4,
    created_at            timestamptz       DEFAULT CURRENT_TIMESTAMP,
    updated_at            timestamptz       DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (schema_name, table_name)
);

CREATE TABLE IF NOT EXISTS partman.tenants
(
    id              VARCHAR NOT NULL,
    parent_table_id VARCHAR NOT NULL,
    created_at      timestamptz DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (parent_table_id, id),
    FOREIGN KEY (parent_table_id)
        REFERENCES partman.parent_tables (id) ON DELETE CASCADE,
    UNIQUE (parent_table_id, id)
);

CREATE TABLE IF NOT EXISTS partman.partitions
(
    id                    VARCHAR PRIMARY KEY,
    name                  VARCHAR     NOT NULL unique,
    parent_table_id       VARCHAR     NOT NULL,
    tenant_id             VARCHAR,
    partition_by          VARCHAR     NOT NULL,
    partition_type        VARCHAR     NOT NULL,
    partition_bounds_from TIMESTAMPTZ NOT NULL,
    partition_bounds_to   TIMESTAMPTZ NOT NULL,
    is_default            BOOLEAN     NOT NULL DEFAULT false,
    status                VARCHAR     NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'detached', 'dropped')),
    created_at            TIMESTAMPTZ          DEFAULT CURRENT_TIMESTAMP,
    updated_at            TIMESTAMPTZ          DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (parent_table_id)
        REFERENCES partman.parent_tables (id) ON DELETE CASCADE,
    FOREIGN KEY (parent_table_id, tenant_id)
        REFERENCES partman.tenants (parent_table_id, id) ON DELETE CASCADE,
    UNIQUE (parent_table_id, tenant_id, partition_bounds_from, partition_bounds_to)
);

CREATE UNIQUE INDEX IF NOT EXISTS partman_default_partition_unique
    ON partman.partitions (parent_table_id, tenant_id)
    NULLS NOT DISTINCT
    WHERE is_default = true;

CREATE OR REPLACE FUNCTION partman.validate_tenant_id() RETURNS TRIGGER AS
$$
BEGIN
    IF NEW.tenant_id IS NOT NULL THEN
        IF NOT EXISTS (SELECT 1
                       FROM partman.tenants
                       WHERE parent_table_id = NEW.parent_table_id
                         AND id = NEW.tenant_id) THEN
            RAISE EXCEPTION 'Tenant % does not exist for parent table %',
                NEW.tenant_id, NEW.parent_table_id;
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS validate_tenant_id_trigger ON partman.partitions;

CREATE TRIGGER validate_tenant_id_trigger
    BEFORE INSERT
    ON partman.partitions
    FOR EACH ROW
EXECUTE FUNCTION partman.validate_tenant_id();
