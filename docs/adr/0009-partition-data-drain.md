# ADR 0009 — `partition_data` helper: drain the default

- **Epic**: E7
- **Status**: Implemented (commit `b6c217a`)
- **Depends on**: 0001, 0004, 0007
- **Blocks**: none

## Context

The default partition catches rows that no bounded partition accepts.
Late-arriving data (rows for future days that arrive early), clock
skew, or a lagging Provisioner all put rows in `_default`. Without a
drain, `_default` grows unbounded and defeats the point of
partitioning: queries with time predicates still scan `_default`.

pg_partman offers `partition_data_proc`. It calls
`partition_data_time` or `partition_data_id` in a loop, batch by
batch, committing per batch. The new library needs the same operator
tool.

The old library at `/Users/rtukpe/Documents/dev/gopartman/` has no
equivalent. Rows that landed in `_default` before a partition existed
stay there forever.

## Decision

- One entry point:
  ```go
  Manager.PartitionData(ctx context.Context, parent ParentRef,
      opts ...DrainOption) (DrainReport, error)
  ```
- `DrainOption` supports:
  - `WithBatchSize(int)` — default 1000.
  - `WithMaxBatches(int)` — default 0 (unlimited).
  - `WithTenant(string)` — drain a single tenant only.
- Steps per batch:
  1. Acquire the advisory lock for the parent using the same key as
     E5's Maintainer: `(hashtext(schema), hashtext(table))`. This
     blocks Retention DDL while the drain runs. Retention is safe
     because it never touches `_default`, but the lock prevents
     concurrent DDL on this parent.
  2. Load the parent row. Read `partition_by` (the control column
     name) and `tenant_column`.
  3. Read the FIRST `BatchSize` rows from the default partition,
     ordered by the control column:
     ```
     SELECT ctid, <control_col> [, <tenant_col>]
     FROM <schema>.<parent>_default
     ORDER BY <control_col>
     LIMIT <batch_size>
     FOR UPDATE SKIP LOCKED
     ```
  4. Group rows by target partition bounds. Compute the bounds from
     the row's control-column value plus the parent's
     `partition_interval`.
  5. For each group:
     - Look up the target partition in `partman.partitions`
       (matching parent, tenant, and bounds).
     - If the target partition EXISTS:
       - Open a transaction.
       - Emit:
         ```
         WITH moved AS (
             DELETE FROM <schema>.<parent>_default
             WHERE ctid = ANY($1::tid[])
             RETURNING *
         )
         INSERT INTO <schema>.<target> SELECT * FROM moved
         ```
       - Commit.
     - If the target partition does NOT exist:
       - Record an anomaly with the missing bounds.
       - Leave the rows in `_default`.
  6. Repeat until the default partition is empty or `MaxBatches` is
     reached.
  7. Release the advisory lock.
- The drain does NOT auto-create missing partitions. Anomalies point
  to a Provisioner or clock skew problem. Fail loud.
- The drain is safe to interrupt. Each batch commits per-target-group.
  A canceled context between groups leaves the rows either fully in
  `_default` or fully in the target — never split.
- Report shape:
  ```go
  type DrainReport struct {
      RowsMoved  int
      BatchesRun int
      Anomalies  []DrainAnomaly
  }

  type DrainAnomaly struct {
      MissingPartitionBounds Bounds
      TenantId               string
      RowCount               int
  }
  ```
- Emit metrics:
  - `partman.drain_rows_moved_total` (tags: `parent`, `tenant`)
  - `partman.drain_batches_total` (tags: `parent`)
  - `partman.drain_anomalies_total` (tags: `parent`, `tenant`)
  - `partman.drain_duration_seconds` (histogram)

## Consequences

- Operators run drain on demand. It is NOT on the maintenance hot
  path.
- The advisory lock prevents Retention DDL races.
- Anomalies are visible. Operators fix the Provisioner or clock and
  re-run drain.
- The drain uses `ctid` for the DELETE to avoid re-scanning the
  control column. This is safe under the `FOR UPDATE SKIP LOCKED`
  read lock.

## Deliverables

- `internal/drain/drain.go` — the drain implementation.
- `internal/drain/sql.go` — the batched SQL builder.
- `internal/drain/drain_test.go` — unit tests with a fake DB.
- `internal/drain/integration_test.go` — real PG tests using the E1c
  harness. Includes:
  - Drain 10k rows from `_default`.
  - Cancel mid-drain and verify consistency.
  - Missing-partition anomaly.

## Acceptance

- Draining 10k rows in `_default` with `BatchSize=1000` produces
  ~10 commits and empties the default partition.
- A canceled `ctx` mid-drain leaves the DB consistent. `_default` and
  the target partition together hold every original row exactly once.
- Rows without a target partition remain in `_default` and appear as
  anomalies in the report.
- The advisory lock is held for the whole drain. A concurrent
  `Maintain(ctx)` for the same parent skips the parent's maintenance
  during the drain.
- `PartitionData` on a parent with no rows in `_default` returns
  `{RowsMoved: 0, BatchesRun: 0}` and no error.

## Open questions

- Auto-invoke from the Maintainer? **Decision: no.** Opt-in later via
  `WithAutoDrain(true)`.
- Auto-create missing partitions? **Decision: no.** Fail loud.
- Composite tenant — drain per tenant or in one pass? **Decision: one
  pass**; group rows by `(tenant, bounds)`.
- What if the control column is nullable and a row has `NULL`?
  **Decision: leave in `_default` and record as an anomaly.**
- Should the drain acquire a row-level lock inside the target
  partition too? **Decision: no.** The `DELETE ... RETURNING` inside
  a transaction is atomic for the batch.

## References

- `CONTEXT.md` — glossary (`partition_data` drain, Anomaly, Advisory
  Lock Key).
- ADR-0001 — `Manager.PartitionData` and `DrainOption`.
- ADR-0004 — target partition creation semantics.
- ADR-0006 — Retention never touches `_default`.
- ADR-0007 — advisory lock key format.
- pg_partman `partition_data_proc` — reference for the batch loop.
