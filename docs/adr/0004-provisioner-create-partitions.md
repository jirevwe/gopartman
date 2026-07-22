# ADR 0004 — Provisioner: create partitions and the default

- **Epic**: E2
- **Status**: Implemented (commit `f523e04`)
- **Depends on**: 0001, 0002, 0003
- **Blocks**: 0005, 0007, 0009

## Context

This is the first epic that emits real DDL. Every downstream epic
consumes what Provisioner produces:

- E3 (Registry) calls Provisioner during `RegisterParent` and
  `RegisterTenant`.
- E5 (Maintainer) calls Provisioner on every tick.
- E7 (Drain) requires the target partitions to exist before it moves
  rows out of `_default`.

The old library at `/Users/rtukpe/Documents/dev/gopartman/manager.go`
lines 135 to 199 creates future partitions in a loop, one transaction
per partition. It does not create the default partition. It formats
SQL with `fmt.Sprintf` (SQL injection risk).

The new library must:

- Create current + `premake` future partitions in ONE transaction per
  call.
- Create the `_default` partition exactly once per parent.
- Be idempotent: calling `EnsurePartitions` twice with the same clock
  produces no DDL on the second call.
- Reconcile: after a partial DDL failure, the next call finishes the
  work.
- Emit safe SQL. Identifiers go through `TableName.Build` or
  `pgx.Identifier{}.Sanitize()`. Values go through parameter binding.

`types.go` declares `PartitionMonthInterval = time.Hour * 24 * 30`.
That is not a month. Monthly partitions must align to calendar month
boundaries, not fixed 30-day windows. This ADR calls that out.

## Decision

- One entry point:
  ```go
  Provisioner.EnsurePartitions(ctx context.Context, parent ParentRef,
      tenant *TenantRef) (EnsureReport, error)
  ```
  `tenant` is `nil` when the parent has no `TenantColumn`.
- Steps in `EnsurePartitions`:
  1. Load the parent row from `partman.parent_tables`.
  2. Compute required bounds. Start from the current period (via
     `Clock.Now()`). Add `parent.premake` future periods.
  3. Query `partman.partitions` for existing bounds under the parent
     (and tenant, if set) with `status = 'active'`.
  4. Diff. Determine which bounds are new.
  5. Check `GetDefaultPartition`. If not tracked, add the default to
     the create list.
  6. Open ONE transaction (`BEGIN`).
  7. For each new bounded partition, emit
     `CREATE TABLE IF NOT EXISTS <fq> PARTITION OF <parent_fq>
     FOR VALUES FROM (...) TO (...)`.
  8. For a new default partition, emit
     `CREATE TABLE IF NOT EXISTS <fq> PARTITION OF <parent_fq>
     DEFAULT`.
  9. Upsert metadata rows in the same transaction via
     `partitions.UpsertPartition` and, for the default, a variant that
     sets `is_default = true`.
  10. `COMMIT`. On any error, `ROLLBACK`.
- All partition names use `TableName.Build()`. No other name
  generator exists.
- Bounds are half-open `[From, To)`:
  - Day interval: `[00:00 UTC of day, 00:00 UTC of next day)`.
  - Week interval: `[00:00 UTC of Monday, 00:00 UTC of next Monday)`.
  - Month interval: `[00:00 UTC of 1st, 00:00 UTC of 1st of next
    calendar month)`. This is a change from the 30-day heuristic in
    `types.go`. Update `types.go` in this epic or introduce an
    interval-arithmetic helper package.
  - Hour interval: `[HH:00 UTC, HH+1:00 UTC)`.
- Tenant partitions use composite `FOR VALUES`:
  ```
  FROM ('<TENANT>', '<from>'::timestamptz)
  TO   ('<TENANT>', '<to>'::timestamptz)
  ```
- The Provisioner takes NO advisory lock. The caller holds the lock:
  - E5 (Maintainer) holds it during the maintenance run.
  - E3 (Registry) does not hold it; registration is human-driven and
    infrequent. Concurrent calls to `RegisterParent` for the same
    parent race, but only one wins the `UNIQUE (schema_name,
    table_name)` constraint.
- On any DDL failure mid-loop, the transaction rolls back. The next
  call retries the entire set.
- Emit metrics via the `Meter` interface (from E8):
  - `partman.partitions_created_total` (tags: `parent`, `tenant`)
  - `partman.default_partitions_created_total` (tags: `parent`,
    `tenant`)
  - `partman.provisioner_duration_seconds` (histogram)

## Consequences

- Downstream epics never issue partition DDL.
- Idempotency means E5 can call `EnsurePartitions` on every tick
  without side effects.
- Half-open bounds match PostgreSQL semantics. There is no
  off-by-one.
- Calendar-month arithmetic differs from the legacy library's 30-day
  math. Callers who relied on the legacy behavior will see partition
  names shift.
- The single-transaction rule means one bad partition rolls back all
  new partitions for that call. The rule is simple. The trade-off is
  documented.

## Deliverables

- `internal/provisioner/provisioner.go` — the `Provisioner`
  implementation.
- `internal/provisioner/sql.go` — DDL string builders. Only uses
  `pgx.Identifier{}.Sanitize()` and parameter binding.
- `internal/provisioner/bounds.go` — compute next-N bounds from an
  interval. Public functions: `NextBoundsUTC(now time.Time, interval
  time.Duration, count int) []Bounds` and per-interval helpers.
- `internal/provisioner/provisioner_test.go` — unit tests for the
  bounds math.
- `internal/provisioner/integration_test.go` — real PG tests using
  the E1c harness.
- Update `types.go` — replace `PartitionMonthInterval` with a signal
  type or add a `CalendarMonth` sentinel that Provisioner recognizes.

## Acceptance

- Calling `EnsurePartitions` twice with the same clock produces zero
  DDL on the second call. Verified by counting entries in
  `pg_stat_statements` or by inspecting `pg_class` before/after.
- `_default` is created exactly once per parent (enforced by the
  partial unique index from ADR-0002).
- Month partitions align to calendar month boundaries. A test at
  `2026-01-31T23:59:59Z` produces partitions for January, February,
  ..., not fixed 30-day chunks.
- Day partitions align to `00:00 UTC`.
- Tenant partitions include the tenant in `FOR VALUES`.
- The `validate_tenant_id` trigger accepts partitions inserted by
  Provisioner.
- On a forced mid-loop DDL failure (test injects a broken bound), the
  transaction rolls back. `pg_class` shows no partial partitions.
  Metadata shows no new rows.
- After a rollback, a second call finishes the work.

## Open questions

- Monthly interval — calendar month or fixed 30 days? **Decision:
  calendar month.** Different from the legacy library. Call it out in
  the migration notes.
- Timezone — UTC only or per parent? **Decision: UTC only for v1.**
  Simpler; matches pg_partman recommendation.
- On mid-loop DDL failure — roll back all or commit successful bounds?
  **Decision: roll back all.** Simpler recovery model.
- Should Provisioner accept a `DryRun` option? **Decision: not for
  v1.** Retention gets dry-run; Provisioner does not. Add later if
  operators ask.
- Do we call `ANALYZE` on the parent after new children land?
  **Decision: no for v1.** pg_partman does it; we can add opt-in
  later.

## References

- `CONTEXT.md` — glossary (Bounds, Premake, Default Partition).
- ADR-0001 — `Manager` / `Provisioner` interface shape.
- ADR-0002 — metadata columns and the default-partition unique
  index.
- pg_partman `create_parent` — the reference for partition creation
  semantics.
- Old library: `/Users/rtukpe/Documents/dev/gopartman/manager.go`
  lines 135 to 199 and 316 to 364 — behavior reference, not code
  reference.
