-- Any pre-existing retention_period values must be Postgres-INTERVAL-parseable
-- (e.g. '7 days', '30 days'). Non-parseable strings fail the ALTER.
ALTER TABLE partman.parent_tables
    ALTER COLUMN retention_period TYPE INTERVAL USING retention_period::interval;
