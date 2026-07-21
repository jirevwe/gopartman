# ADR 0006 — Retention: drop or detach with Hook

- **Epic**: E4
- **Status**: Proposed
- **Depends on**: 0001, 0002, 0004
- **Blocks**: 0007

## Context

The Provisioner (ADR-0004) creates partitions forever. Production
needs cleanup. E5 (Maintainer) needs a Retention interface to call.

The old library at `/Users/rtukpe/Documents/dev/gopartman/manager.go`
lines 212 to 301 drops old partitions in a loop. It has these problems:

- It logs and continues on error. Silent failures are common.
- It has no way to detach instead of drop. Once a partition is old,
  it is dead.
- It calls a pre-drop hook with no deadline (line 261 has a TODO
  comment). One slow hook stalls the whole loop.
- It cannot move partitions to an archive schema.

The new library must:

- Never touch the default partition.
- Never touch current or future partitions.
- Offer three fates via a Hook: drop, detach, archive.
- Fail loud when config is inconsistent (archive schema missing).
- Emit typed errors and metrics.

## Decision

- One entry point:
  ```go
  Retention.Sweep(ctx context.Context, parent ParentRef,
      opts ...SweepOption) (SweepReport, error)
  ```
  `SweepOption` supports `WithDryRun(bool)`.
- Selection query (via sqlc `ListExpiredPartitions` from ADR-0002):
  ```
  SELECT ... FROM partman.partitions
  WHERE parent_table_id = $1
    AND is_default = false
    AND status = 'active'
    AND partition_bounds_to <= $2
  ```
  `$2` is `Clock.Now() - parent.retention_period`.
- Invariants (documented, tested, enforced by the SQL filter):
  - The default partition is NEVER a candidate.
  - The current period is NEVER a candidate (`bounds_to <= now -
    retention` filters it out).
  - Future periods are NEVER candidates.
- For each candidate:
  1. Build a `PartitionRef` from the metadata row.
  2. Call the `Hook`. When no hook is configured, treat as
     `HookDrop`.
  3. Apply the decision under one transaction per partition:
     - `HookDrop`: `DROP TABLE <fq> CASCADE`. Then
       `MarkPartitionDropped`.
     - `HookDetach`: `ALTER TABLE <parent_fq> DETACH PARTITION
       <fq>`. Then `MarkPartitionDetached`.
     - `HookArchive`: detach, then
       `ALTER TABLE <fq> SET SCHEMA <retention_schema>`. Then
       `MarkPartitionDetached`. Fail with `ErrArchiveSchemaMissing`
       if `retention_schema` is empty on the parent row.
     - `HookSkip`: log, leave the partition. The next sweep
       re-offers it.
  4. On error inside the transaction, roll back that partition's tx
     and continue to the next candidate. Do NOT stop the sweep.
- Dry-run mode:
  - When `SweepOption` sets dry-run, the sweep still calls the Hook
    but performs NO DDL and NO metadata write. The `SweepReport`
    contains the intended fates.
- Retention DOES NOT create the archive schema. Fail loud when the
  schema is missing.
- A separate method `Retention.DropAll(ctx, parent ParentRef) error`
  is used by E3 (`RemoveParent` with `WithCascadeDrop`). It ignores
  the Hook. It drops every child of the parent, including the
  default. It marks every metadata row `status = 'dropped'`.
- Retention takes NO advisory lock. E5 (Maintainer) holds it.
- Emit metrics:
  - `partman.partitions_dropped_total` (tags: `parent`)
  - `partman.partitions_detached_total` (tags: `parent`)
  - `partman.partitions_archived_total` (tags: `parent`,
    `archive_schema`)
  - `partman.retention_skipped_total` (tags: `parent`, `reason`)
  - `partman.retention_duration_seconds` (histogram)
- Report shape:
  ```go
  type SweepReport struct {
      Considered int
      Dropped    []PartitionRef
      Detached   []PartitionRef
      Archived   []PartitionRef
      Skipped    []PartitionRef
      DryRun     bool
  }
  ```

## Consequences

- The Hook is the extension point for archiving to S3, backup, or
  auditing.
- Callers who want "drop only" pass no hook.
- The default partition is safe by construction (SQL filter + Go
  guard).
- Dry-run gives operators a preview before enabling automatic
  retention.
- Retry after crash is safe: the `status` column marks progress. A
  crashed sweep restarts and skips already-processed rows.

## Deliverables

- `internal/retention/retention.go` — the `Retention` implementation.
- `internal/retention/cutoff.go` — compute cutoff from the parent's
  `INTERVAL` column and `Clock.Now()`.
- `internal/retention/retention_test.go` — unit tests with a mock DB
  and mock Hook.
- `internal/retention/integration_test.go` — real PG tests using the
  E1c harness.

## Acceptance

- `_default`, current, and future partitions never appear in the
  expired list. Tested with a clock skew of 10 years.
- `HookSkip` leaves the partition. The next sweep re-offers the same
  partition.
- `HookArchive` with an empty `retention_schema` on the parent
  returns `ErrArchiveSchemaMissing`. The partition stays.
- Metadata `status` transitions correctly: `active -> detached`,
  `active -> dropped`. Idempotent on retry.
- `Sweep` is safe after a mid-sweep crash. A test injects a fault
  after the first `MarkPartitionDropped`. The next `Sweep` finishes
  the rest without redoing the first.
- `DropAll` removes every child including the default. The metadata
  is consistent.
- Dry-run produces the same `SweepReport` shape but no DDL and no
  metadata writes.

## Open questions

- Hook returns an error — retry now or skip and let next tick retry?
  **Decision: skip.** Simpler failure model. Operators fix the hook
  and wait one tick.
- `retention_schema` — auto-create if missing? **Decision: no.**
  Fail loud.
- Does Retention accept a "dry run" mode? **Decision: yes.** Small
  addition, big operator value. Locked in the Decision section above.
- Do we bound the number of partitions handled per sweep? **Decision:
  no for v1.** If a sweep processes thousands of partitions, we add
  batching later.
- Does the Hook get called for `DropAll`? **Decision: no.**
  `DropAll` is a cascade-remove operation; the operator already
  decided.

## References

- `CONTEXT.md` — glossary (Detach, Drop, Archive Schema, Pre-Drop
  Hook).
- ADR-0001 — `Hook`, `HookDecision`, `PartitionRef`, `Retention`
  interface.
- ADR-0002 — `status` column, `retention_schema`,
  `retention_keep_table`, `ListExpiredPartitions`.
- ADR-0004 — Provisioner never touches existing partitions.
- pg_partman `retention` and `retention_schema` — reference for the
  drop-vs-detach split.
- Old library: `/Users/rtukpe/Documents/dev/gopartman/manager.go`
  lines 212 to 301 — behavior reference for the bugs we avoid.
