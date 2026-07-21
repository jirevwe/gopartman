# ADR 0005 — Registry lifecycle

- **Epic**: E3
- **Status**: Proposed
- **Depends on**: 0001, 0002, 0004
- **Blocks**: 0007

## Context

The Provisioner (ADR-0004) creates partitions but has no lifecycle. It
does not validate anything. It does not know if a parent exists in
PostgreSQL. It does not know if a tenant belongs to a parent.

The Registry owns:

- `RegisterParent` — insert a parent metadata row after strict
  validation, then trigger the first `EnsurePartitions` call.
- `RegisterTenant` — insert a tenant row, then trigger
  `EnsurePartitions` for the new tenant.
- `RemoveParent` and `RemoveTenant` — delete metadata; optionally
  cascade to PostgreSQL.
- `ListParents`, `ListTenants` — read metadata.

The old library at `/Users/rtukpe/Documents/dev/gopartman/manager.go`
does registration in the same file as everything else. That mix is
what the split-by-concern decision avoids.

The user chose "the target table must already exist and be
`PARTITION BY RANGE`". The library does not create parent tables. This
matches pg_partman's `create_parent`, which requires an existing
partitioned parent.

## Decision

- `Registry` is one interface, implemented by `internal/registry`.
- `RegisterParent(ctx, cfg ParentConfig) error`.
  - `ParentConfig` fields:
    ```
    Schema                string
    Table                 string
    TenantColumn          string          // empty when no tenant
    PartitionBy           string          // time column name
    PartitionInterval     time.Duration
    Premake               int             // 0 => default 4
    RetentionPeriod       time.Duration   // 0 => keep all
    RetentionKeepTable    bool
    RetentionSchema       string          // empty => no archive
    AutomaticMaintenance  bool            // default true
    ```
  - Steps:
    1. Validate identifiers with the `TableName` regex from `table.go`
       (only `\w+`).
    2. Confirm the target table exists in PostgreSQL
       (`pg_class` + `pg_namespace`).
    3. Confirm the target table is `PARTITION BY RANGE`
       (`pg_partitioned_table.partstrat = 'r'`).
    4. Confirm `PartitionBy` column exists on the target table.
    5. Confirm `TenantColumn` exists on the target table if set.
    6. Confirm `RetentionSchema` exists in PostgreSQL if set (do NOT
       create it).
    7. Insert into `partman.parent_tables` via
       `parents.UpsertParentTable`.
    8. Call `Provisioner.EnsurePartitions(ctx, parentRef, nil)` (no
       tenant) — this creates the `_default` partition plus the
       initial period + `premake` bounded partitions when the parent
       has no `TenantColumn`.
    9. When the parent HAS a `TenantColumn`, skip step 8. Provisioner
       runs per-tenant only.
- `RegisterTenant(ctx, cfg TenantConfig) error`.
  - `TenantConfig`:
    ```
    ParentSchema string
    ParentTable  string
    TenantId     string
    ```
  - Steps:
    1. Load the parent row. If not found, return `ErrParentNotFound`.
    2. If the parent has no `TenantColumn`, return
       `ErrParentNotTenanted`.
    3. Validate `TenantId` with the `TableName` regex.
    4. Insert into `partman.tenants` via `tenants.UpsertTenant`.
    5. Call `Provisioner.EnsurePartitions(ctx, parentRef, &tenantRef)`.
- `RemoveTenant(ctx, ref TenantRef) error`.
  - Deletes the row in `partman.tenants`. The FK cascade from
    ADR-0002 removes the tenant's partition metadata. The PG
    partition tables STAY. Operators drop them later or leave them
    for archival.
- `RemoveParent(ctx, ref ParentRef, opts ...RemoveOption) error`.
  - Default: deletes the parent metadata row. FK cascade removes
    tenants and partition metadata. PG partition tables stay.
  - With `WithCascadeDrop`, the Registry calls
    `Retention.DropAll(ctx, ref)` BEFORE the metadata delete. This
    drops every PG child (including the default).
- `ListParents(ctx) ([]ParentInfo, error)`.
- `ListTenants(ctx, ref ParentRef) ([]TenantInfo, error)`.
- Registry emits typed errors (from E8):
  `ErrParentNotFound`, `ErrTenantNotFound`, `ErrParentAlreadyExists`,
  `ErrTenantAlreadyExists`, `ErrTargetNotPartitioned`,
  `ErrColumnMissing`, `ErrParentNotTenanted`, `ErrArchiveSchemaMissing`.

## Consequences

- Registration is validation plus delegation. No DDL logic sits here.
- Remove operations default to metadata-only. That is reversible; the
  operator can re-register.
- FK cascades keep metadata consistent without extra Go code.
- The Registry never creates the target table. That is the operator's
  job. This matches pg_partman.

## Deliverables

- `internal/registry/registry.go` — the `Registry` implementation.
- `internal/registry/validate.go` — column existence checks,
  partition-strategy check, name checks, archive-schema existence
  check.
- `internal/registry/registry_test.go` — unit tests with a mock
  Provisioner.
- `internal/registry/integration_test.go` — real PG tests using the
  E1c harness.
- New queries in `internal/parents/queries.sql`:
  `DeleteParentTable :exec`.
- New queries in `internal/tenants/queries.sql`:
  `UpsertTenant :exec`, `DeleteTenant :exec`.

## Acceptance

- `RegisterParent` on a target table without the `PartitionBy` column
  returns `ErrColumnMissing`.
- `RegisterParent` on a non-partitioned target returns
  `ErrTargetNotPartitioned`.
- `RegisterParent` with a `RetentionSchema` that does not exist
  returns `ErrArchiveSchemaMissing`.
- `RegisterTenant` provisions current + `premake` future partitions for
  the new tenant only. Other tenants keep their existing partition
  count.
- `RegisterTenant` on a parent with no `TenantColumn` returns
  `ErrParentNotTenanted`.
- `RemoveTenant` leaves PG partition tables in place. Metadata rows for
  that tenant are gone.
- `RemoveParent` with `WithCascadeDrop` drops all child tables
  including the default.
- `validate_tenant_id` trigger from ADR-0002 fires when a caller
  bypasses the Registry.

## Open questions

- Should `RegisterParent` accept a `[]TenantConfig` to bulk-add
  tenants? **Decision: no for v1.** Callers loop.
- Should `RemoveParent` fail if partition metadata rows exist without
  cascade? **Decision: no.** FK handles it.
- Does `RemoveTenant` support `WithCascadeDrop` too? **Decision: yes.**
  Same semantics.
- Is the Registry safe to call concurrently for the same parent?
  **Decision: yes.** The `UNIQUE (schema_name, table_name)`
  constraint serializes.

## References

- `CONTEXT.md` — glossary (Parent, Tenant, Archive Schema).
- ADR-0001 — `Registry` interface shape.
- ADR-0002 — FK cascades and the `validate_tenant_id` trigger.
- ADR-0004 — Provisioner call semantics.
- pg_partman `create_parent` — reference for the "target must
  already be partitioned" rule.
