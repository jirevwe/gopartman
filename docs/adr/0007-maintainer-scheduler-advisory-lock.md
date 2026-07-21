# ADR 0007 — Maintainer, scheduler, and advisory lock

- **Epic**: E5
- **Status**: Proposed
- **Depends on**: 0001, 0004, 0005, 0006
- **Blocks**: 0008, 0009

## Context

E2, E3, and E4 give the library primitives but no autonomous loop.
Production needs a scheduler that ticks, provisions, and retains
without operator intervention.

The user chose "In-process ticker with PG advisory lock" over a
single-instance ticker. That means two app replicas can run the
library against the same database. Only one performs maintenance on
a given parent per tick. The other skips.

The old library at `/Users/rtukpe/Documents/dev/gopartman/manager.go`
lines 483 to 500 has a single-goroutine `time.Ticker` loop. It has no
advisory lock. Running two replicas causes conflicts (duplicate
`CREATE TABLE` failures, double retention passes).

## Decision

- One entry point per method on `Maintainer`:
  ```go
  Start(ctx context.Context) error
  Stop(ctx context.Context) error
  Maintain(ctx context.Context) error
  ```
- `Start` spins ONE goroutine. The goroutine ticks on `time.Ticker`
  seeded from the `WithScheduleInterval` option (default `1h`).
- On each tick, the loop calls `Maintain(ctx)` synchronously in the
  scheduler goroutine.
- `Maintain(ctx)` steps:
  1. Load all parents via `Registry.ListParents(ctx)`.
  2. For each parent where `automatic_maintenance = true`:
     - Compute the advisory lock key: `key1 = hashtext(schema)`,
       `key2 = hashtext(table)` (both `int4`).
     - Call `SELECT pg_try_advisory_lock($key1, $key2)`. If it
       returns `false`, log at `INFO`, emit
       `partman.lock_skipped_total`, and skip this parent for this
       tick.
     - Under the lock, run a deferred `pg_advisory_unlock($key1,
       $key2)` and:
       - Load tenants via `Registry.ListTenants(ctx, parentRef)`.
       - If the parent has no `TenantColumn`, call
         `Provisioner.EnsurePartitions(ctx, parentRef, nil)`.
       - Else, iterate tenants and call
         `Provisioner.EnsurePartitions(ctx, parentRef, &tenantRef)`
         for each.
       - Call `Retention.Sweep(ctx, parentRef)`.
     - Release the lock.
  3. Panics inside one parent's work do NOT kill the loop. Wrap the
     per-parent block in a `defer recover()`. Log the panic. Move to
     the next parent.
- `Stop(ctx)` closes a `done` channel. It waits for the in-flight
  tick via `sync.WaitGroup`. It respects the `ctx` deadline: if `ctx`
  expires while a tick is running, `Stop` returns the `ctx` error and
  leaves the loop to finish its current parent before the next tick
  never fires. The `sync.WaitGroup` prevents `Stop` from returning
  before the goroutine actually exits.
- `Maintain(ctx)` is also public. Tests and one-off scripts call it
  directly. `Manager.Maintain` delegates to it.
- Process parents sequentially in one tick for v1. Parallel per-parent
  goroutines are a follow-up.
- Emit metrics:
  - `partman.maintenance_runs_total`
  - `partman.maintenance_duration_seconds` (histogram)
  - `partman.lock_skipped_total` (tags: `parent`)
  - `partman.parents_processed_total` (tags: `parent`)
  - `partman.parents_panicked_total` (tags: `parent`)
- Emit structured logs at `INFO`:
  - Tick start: `tick_id`, `parent_count`.
  - Per parent: `parent`, `duration_ms`, `partitions_created`,
    `partitions_dropped`, `partitions_detached`, `partitions_archived`.
  - Tick end: `tick_id`, `total_duration_ms`, `skipped_count`.

## Consequences

- Two replicas can run the library against the same database. Only
  one processes a given parent per tick. The other skips cleanly.
- A bad parent does not block the good parents. Panic recovery
  isolates faults.
- `Maintain(ctx)` gives a test-friendly entry point. Simulated clock
  drives the tests.
- The advisory-lock key uses `hashtext`. Collisions are rare in
  practice (schema+table pair) but not impossible. Document that
  callers who need absolute safety use unique schema+table pairs
  (which the `UNIQUE (schema_name, table_name)` constraint already
  enforces).

## Deliverables

- `internal/maintainer/maintainer.go` — the `Maintainer` interface
  and implementation.
- `internal/maintainer/scheduler.go` — the ticker loop.
- `internal/maintainer/advisory_lock.go` — the `TryLock` and
  `Unlock` helpers.
- `internal/maintainer/maintainer_test.go` — unit tests using
  simulated clock and mocked `Registry`, `Provisioner`, `Retention`.
- `internal/maintainer/integration_test.go` — real PG tests using
  the E1c harness. Includes a test that spins two `Maintainer`s
  against one DB and asserts exactly one processes each parent per
  tick.

## Acceptance

- Two processes maintain the same DB. Exactly one runs a given
  parent's maintenance per tick. Verified by inspecting metrics or
  logs across both processes.
- `Stop(ctx)` returns before the `ctx` deadline even mid-run. The
  loop goroutine has exited.
- A `panic()` injected inside `Provisioner.EnsurePartitions` for one
  parent does not kill the scheduler. The other parents run.
- A parent with `automatic_maintenance = false` is skipped. No lock
  traffic. No metrics for that parent this tick.
- `Maintain(ctx)` called directly (without `Start`) does the full
  round.
- The simulated clock in tests can advance a full retention window
  and observe drops on the next tick.

## Open questions

- Advisory lock key — `(hashtext(schema), hashtext(table))` or single
  `bigint`? **Decision: dual-int32.** Matches PostgreSQL's
  `pg_try_advisory_lock(int, int)` overload. Two-value keying gives
  us schema-level and table-level dimensions.
- Default tick interval? **Decision: 1 hour.** Matches pg_partman
  BGW.
- Process parents in parallel or sequentially? **Decision:
  sequentially for v1.** Simpler. Revisit if we see contention.
- Log every tick or only failures? **Decision: structured INFO with
  counts.** Operators want to see health.
- Should the loop expose a channel or callback for each tick's
  report? **Decision: no for v1.** Metrics and logs cover the need.
- What if a tick takes longer than the interval? **Decision: the
  `time.Ticker` drops the intervening tick automatically.** Log a
  warning when this happens.

## References

- `CONTEXT.md` — glossary (Maintenance Run, Advisory Lock Key).
- ADR-0001 — `Maintainer` interface and `Manager.Maintain` delegation.
- ADR-0004 — Provisioner's idempotency: multiple ticks are safe.
- ADR-0005 — `Registry.ListParents`, `Registry.ListTenants`.
- ADR-0006 — `Retention.Sweep`.
- pg_partman background worker semantics — reference for the
  run-maintenance loop pattern.
