# ADR 0002 — Metadata schema extension

- **Epic**: E1b
- **Status**: Proposed
- **Depends on**: 0000
- **Blocks**: 0004, 0005, 0006, 0007, 0008

## Context

The current `schema.sql` defines three tables in the `partman` schema:
`parent_tables`, `tenants`, `partitions`. The user chose "Extend the
current schema" rather than redesign. Extending in one migration means
downstream epics only add queries — they never touch columns.

Downstream epics need these columns that do not exist today:

- E2 (Provisioner) needs `premake` on `parent_tables` and `is_default`
  on `partitions`.
- E4 (Retention) needs `retention_keep_table` and `retention_schema`
  on `parent_tables`, and `status` on `partitions`.
- E5 (Maintainer) needs `automatic_maintenance` on `parent_tables`.
- E4 and E7 both read a `status` field to know which partitions are
  live.

The current `retention_period` column is `VARCHAR`. That forces
runtime parsing every time. PostgreSQL has a native `INTERVAL` type.
Use it.

The current `partition_count` column is a legacy from the old library.
It is not used in the new design. `premake` replaces it.

The old library at `/Users/rtukpe/Documents/dev/gopartman/queries.go`
lines 51 to 67 has a `validate_tenant_id` trigger and function. The
new library needs the same protection.

`sqlc.yaml` runs in `database.managed: true` mode. That handles
codegen. It does NOT handle runtime migrations. Library consumers must
apply migrations themselves. The library must expose them.

## Decision

- Extend `partman.parent_tables` with these columns:
  - `premake INT NOT NULL DEFAULT 4`
  - `retention_keep_table BOOLEAN NOT NULL DEFAULT false`
  - `retention_schema VARCHAR`
  - `automatic_maintenance BOOLEAN NOT NULL DEFAULT true`
- Change `partman.parent_tables.retention_period` from `VARCHAR` to
  `INTERVAL NOT NULL`.
- Drop `partman.parent_tables.partition_count`. `premake` replaces it.
- Extend `partman.partitions` with these columns:
  - `is_default BOOLEAN NOT NULL DEFAULT false`
  - `status VARCHAR NOT NULL DEFAULT 'active'` with a CHECK constraint
    limiting values to `active`, `detached`, `dropped`.
- Add the `validate_tenant_id` trigger and function. Port from
  `/Users/rtukpe/Documents/dev/gopartman/queries.go` lines 51 to 67.
  The trigger fires on `INSERT` into `partman.partitions`. It rejects
  any row where the `tenant_id` is not null and the pair
  `(parent_table_id, tenant_id)` does not exist in `partman.tenants`.
- Add a partial unique index for the default partition:
  ```sql
  CREATE UNIQUE INDEX partman_default_partition_unique
      ON partman.partitions (parent_table_id, tenant_id)
      WHERE is_default = true;
  ```
  This enforces "one default partition per (parent, tenant)".
- Adopt a versioned migration layout under `migrations/`:
  - `migrations/0001_init.sql` — the current baseline (copy from
    `schema.sql`).
  - `migrations/0002_add_premake_and_retention_columns.sql`
  - `migrations/0003_add_is_default_and_status.sql`
  - `migrations/0004_add_validate_tenant_trigger.sql`
  - `migrations/0005_change_retention_period_to_interval.sql`
  - `migrations/0006_drop_partition_count.sql`
- Keep `schema.sql` as the "final state" file. `sqlc.yaml` still reads
  it in managed mode for codegen. A smoke test verifies that
  `schema.sql` and the union of `migrations/*.sql` produce the same
  schema.
- Expose migrations via `//go:embed`. Add public function
  `partman.Migrations() []Migration` where `Migration` has fields
  `Version int`, `Name string`, `SQL string`.
- Add these queries to `internal/parents/queries.sql`:
  - `GetParentTable :one` — by `(schema_name, table_name)`.
  - `ListParentTables :many` — all rows.
  - `UpdateAutomaticMaintenance :exec` — flip the flag.
- Add these queries to `internal/partitions/queries.sql`:
  - `ListPartitionsForParent :many` — filtered by
    `parent_table_id`.
  - `MarkPartitionDetached :exec` — set `status = 'detached'`.
  - `MarkPartitionDropped :exec` — set `status = 'dropped'`.
  - `GetDefaultPartition :one` — by `(parent_table_id, tenant_id)`
    and `is_default = true`.
  - `ListExpiredPartitions :many` — filtered by `bounds_to <= $2`,
    `is_default = false`, `status = 'active'`. Used by E4.
- Run `sqlc generate`. Commit both `queries.sql` and generated code.

## Consequences

- E2 through E7 only add queries. No later epic changes columns.
- Library consumers run migrations at startup with
  `partman.Migrations()`. They can adapt to any migration tool.
- The runtime uses the migration files. `sqlc.yaml` uses `schema.sql`
  for codegen. The smoke test keeps them in sync.
- `retention_period` becomes a native `INTERVAL`. No parse code in Go.
- The default-partition unique index prevents duplicates from concurrent
  provisioners.

## Deliverables

- `schema.sql` — extended (final state).
- `migrations/0001_init.sql` through `migrations/0006_...`.
- `migrations.go` — `//go:embed migrations/*.sql`; exports
  `Migration` type and `Migrations()` function.
- Regenerated `internal/parents/repo/*.go`,
  `internal/tenants/repo/*.go`, `internal/partitions/repo/*.go`.
- New queries in `internal/parents/queries.sql` and
  `internal/partitions/queries.sql`.
- Migration-vs-schema smoke test at
  `internal/testsupport/migrations_test.go`.

## Acceptance

- `sqlc generate` succeeds.
- Applying `migrations/*.sql` in order to an empty DB produces the
  same schema as applying `schema.sql`. Verified by a smoke test that
  diffs `information_schema.columns` and
  `information_schema.check_constraints`.
- `validate_tenant_id` trigger rejects an `INSERT INTO
  partman.partitions` with an unknown `tenant_id`.
- The default-partition unique index rejects a second row with
  `is_default = true` for the same `(parent_table_id, tenant_id)`.
- `ListExpiredPartitions` returns zero rows when only the default
  partition exists.
- No column added here is unused by any planned epic (E2 through E7).
  Verified against the epic-column matrix in the plan file.

## Open questions

- Migration runner — library applies or consumer applies? **Decision:
  consumer applies.** Expose `Migrations()`.
- Migration shape — plain `[]Migration` or `golang-migrate` compatible?
  **Decision: plain.** Consumers can adapt.
- Keep `status = 'archived'` as a fourth value? **Decision: no.**
  `detached` is enough. `retention_schema` on the parent tells you if
  it moved.
- Store `retention_schema = ''` or `NULL` when unused? **Decision:
  `NULL`.** Native.
- Should `automatic_maintenance` default to `true` or `false`?
  **Decision: `true`.** Match pg_partman.

## References

- `CONTEXT.md` — glossary (Premake, Retention Window, Default
  Partition, Archive Schema).
- Old library trigger: `/Users/rtukpe/Documents/dev/gopartman/queries.go`
  lines 51 to 67.
- pg_partman `part_config` columns: `premake`, `retention`,
  `retention_schema`, `retention_keep_table`, `automatic_maintenance`.
