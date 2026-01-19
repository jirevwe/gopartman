create schema if not exists partman;

CREATE TABLE IF NOT EXISTS partman.parent_tables
(
    id                 VARCHAR PRIMARY KEY,
    schema_name        VARCHAR NOT NULL,
    table_name         VARCHAR NOT NULL,
    tenant_column      VARCHAR,
    partition_by       VARCHAR NOT NULL,
    partition_type     VARCHAR NOT NULL,
    partition_interval VARCHAR NOT NULL,
    partition_count    INT     NOT NULL DEFAULT 10,
    retention_period   VARCHAR NOT NULL,
    created_at         timestamptz      DEFAULT CURRENT_TIMESTAMP,
    updated_at         timestamptz      DEFAULT CURRENT_TIMESTAMP,
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
    created_at            TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at            TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (parent_table_id)
        REFERENCES partman.parent_tables (id) ON DELETE CASCADE,
    FOREIGN KEY (parent_table_id, tenant_id)
        REFERENCES partman.tenants (parent_table_id, id) ON DELETE CASCADE,
    UNIQUE (parent_table_id, tenant_id, partition_bounds_from, partition_bounds_to)
);