# ADR 0008 — Import and reconcile existing partitions

- **Epic**: E6
- **Status**: Proposed
- **Depends on**: 0001, 0002, 0004
- **Blocks**: none

## Context

Real adopters have existing partitioned tables. They do not want to
drop and re-create. They want to point `go_partman` at what they have
and let it take over.

The old library at `/Users/rtukpe/Documents/dev/gopartman/manager.go`
lines 518 to 663 has an `importExistingPartitions` routine. It parses
range bounds from `pg_get_expr(relpartbound)`. It cannot detect the
default partition. It runs implicitly during `NewManager`, which
surprises operators.

The new library must:

- Run import as an explicit call: `Manager.ImportExisting`.
- Handle bounded partitions AND the default partition.
- Report drift (metadata that no PG table matches).
- NOT silently rewrite user data.

## Decision

- One entry point:
  ```go
  Manager.ImportExisting(ctx context.Context, parent ParentRef)
      (ReconcileReport, error)
  ```
- Steps:
  1. Confirm the parent is registered in `partman.parent_tables`. If
     not, return `ErrParentNotFound`.
  2. Query `pg_inherits + pg_class` for children of the parent:
     ```
     SELECT c.oid, n.nspname, c.relname,
            pg_get_expr(c.relpartbound, c.oid) AS bound_expr
     FROM pg_inherits i
     JOIN pg_class c ON c.oid = i.inhrelid
     JOIN pg_namespace n ON n.oid = c.relnamespace
     WHERE i.inhparent = to_regclass($1)
     ```
  3. For each child:
     - Parse `bound_expr`:
       - `DEFAULT` — default partition.
       - `FOR VALUES FROM (<from>) TO (<to>)` — bounded partition.
       - `FOR VALUES FROM ('<tenant>', <from>) TO ('<tenant>', <to>)`
         — tenant partition.
     - Use `TableName.Parse(schema + "." + relname)` to extract
       `(tenantId, isDefault, bounds)` from the child's NAME.
     - Cross-check the parsed name against the parsed `bound_expr`:
       - If they agree, insert or upsert the metadata row.
       - If they disagree, record a `Drifted` entry with both sides
         and skip the insert.
     - If the child name does not match `TableName.Build`'s format,
       record a `Skipped` entry with a reason (`"non-conforming
       name"`).
  4. Query metadata for partitions that exist in `partman.partitions`
     but not in `pg_class`. Record these as `Orphaned`.
  5. Return `ReconcileReport`.
- `ReconcileReport` shape:
  ```go
  type ReconcileReport struct {
      Imported []PartitionRef
      Drifted  []DriftedPartition
      Orphaned []PartitionRef
      Skipped  []SkippedPartition
  }

  type DriftedPartition struct {
      Name        string
      NameBounds  Bounds
      ActualBound string  // raw pg_get_expr output
      Reason      string
  }

  type SkippedPartition struct {
      Name   string
      Reason string
  }
  ```
- Import takes NO advisory lock. Operators run it once during
  onboarding. Concurrent runs are safe because inserts use `ON
  CONFLICT DO NOTHING` (from ADR-0002's `UpsertPartition`).
- Import does NOT auto-fix `Drifted` or `Orphaned` entries. The
  report is for the operator.
- Import does NOT run implicitly. `Manager.RegisterParent` does not
  call it. Operators call `ImportExisting` explicitly after
  registration when they onboard an existing table.

## Consequences

- The onboarding path is documented and explicit.
- Drift catches DBA changes and provisioning bugs.
- The library does not silently rewrite user data.
- Non-conforming names (partitions created by other tooling) do not
  block onboarding; they appear as `Skipped`.

## Deliverables

- `internal/importer/importer.go` — the importer implementation.
- `internal/importer/parse_bounds.go` — parse the `pg_get_expr`
  string. Handles `DEFAULT`, `FROM (a) TO (b)`, and the composite
  tenant form.
- `internal/importer/parse_bounds_test.go` — unit tests covering
  every parse case.
- `internal/importer/importer_test.go` — unit tests with a fake
  child listing.
- `internal/importer/integration_test.go` — real PG tests using the
  E1c harness. Includes a case where the operator has a mix of
  conforming names, non-conforming names, and orphan metadata.

## Acceptance

- Given a parent with N pre-existing bounded partitions, `ImportExisting`
  produces N metadata rows.
- `_default` is detected via `pg_get_expr` returning `DEFAULT`. The
  metadata row has `is_default = true`.
- Composite tenant bounds parse correctly. The tenant is upper-cased
  before comparison against `TableName.Parse` output.
- `Drifted` fires when the name says one bound and the `pg_get_expr`
  says another.
- `Orphaned` fires when metadata rows have no matching `pg_class`
  entry.
- `Skipped` fires for names that do not match `TableName.Build`'s
  format.
- Calling `ImportExisting` twice against the same DB produces a
  second report with `Imported = []` and the same `Drifted`,
  `Orphaned`, and `Skipped` sets.

## Open questions

- Non-conforming names — skip with a warning or import with a raw
  name? **Decision: skip with a warning** (in `Skipped`). The library
  cannot manage a partition it cannot name.
- Interval mismatch — the parent says `daily` but PG has monthly
  partitions. Fail with `ErrIntervalMismatch` or import anyway?
  **Decision: fail loud with `ErrIntervalMismatch`**. Register with
  the correct interval or fix the PG structure first.
- Should the importer offer a "reconcile fix" mode that removes
  orphaned metadata? **Decision: no for v1.** The operator inspects
  the report and calls `RemoveTenant`/`RemoveParent` or issues a
  manual DELETE.
- Does `ImportExisting` respect the parent's `automatic_maintenance`
  flag? **Decision: no.** Import is operator-driven; the flag
  controls the loop, not one-off calls.

## References

- `CONTEXT.md` — glossary (Reconcile, Anomaly).
- ADR-0001 — `TableName.Parse` inverse of `Build`.
- ADR-0002 — `is_default` column, `ON CONFLICT DO NOTHING` semantics.
- ADR-0004 — Provisioner names via `TableName.Build`. Import assumes
  the same names.
- Old library: `/Users/rtukpe/Documents/dev/gopartman/manager.go`
  lines 518 to 663 — behavior reference for the pg_inherits query.
- pg_partman does not offer a comparable import step. This is a
  new-library addition.
