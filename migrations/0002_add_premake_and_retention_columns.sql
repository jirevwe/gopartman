ALTER TABLE partman.parent_tables
    ADD COLUMN IF NOT EXISTS premake               INT     NOT NULL DEFAULT 4,
    ADD COLUMN IF NOT EXISTS retention_keep_table  BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS retention_schema      VARCHAR,
    ADD COLUMN IF NOT EXISTS automatic_maintenance BOOLEAN NOT NULL DEFAULT true;
