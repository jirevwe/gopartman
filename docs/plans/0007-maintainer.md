# Plan — ADR 0007 Maintainer, scheduler, advisory lock

All prose is ASD-STE100. Short sentences. One idea per sentence.

## Scope

This plan implements ADR 0007. The plan does not add metrics types.
ADR 0010 owns the meter. The plan uses `slog` for structured logs.
The plan leaves the `Meter any` field on `Manager` alone.

## Files to add

- `internal/maintainer/advisory_lock.go` — `TryLock` and `Unlock`.
- `internal/maintainer/scheduler.go` — the `Start` and `Stop` loop.
- `internal/maintainer/maintainer_test.go` — unit tests.
- `internal/maintainer/integration_test.go` — real PG tests.

## Files to edit

- `internal/maintainer/maintainer.go` — replace `type Maintainer any`
  with the interface and the `Impl` type. Add `Config`, `New`, and
  `Maintain`.
- `manager.go` — construct the `Maintainer` in `initInternals`. Wire
  `Start`, `Stop`, and `Maintain` to the field.

## Interface

The `Maintainer` interface has three methods:

```go
type Maintainer interface {
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
    Maintain(ctx context.Context) error
}
```

## Dependencies

The maintainer needs these seams:

- `Registry` — the `ListParents` and `ListTenants` methods only.
- `Provisioner` — the `EnsurePartitions` method only.
- `Retention` — the `Sweep` method only.
- `Clock` — used only for logs and timings (the ticker uses
  `time.Ticker`; not the `Clock`).
- `*pgxpool.Pool` — used only by the advisory-lock helpers.
- `*slog.Logger` — required, but defaults to `slog.Default()`.

The maintainer keeps its own narrow interfaces so the tests can pass
mocks. The concrete `Registry`, `Provisioner`, and `Retention` types
satisfy those interfaces structurally.

## Advisory lock

`TryLock(ctx, tx, schema, table) (locked bool, err error)` runs:

```
SELECT pg_try_advisory_lock(hashtext($1)::int, hashtext($2)::int)
```

`Unlock(ctx, tx, schema, table) error` runs:

```
SELECT pg_advisory_unlock(hashtext($1)::int, hashtext($2)::int)
```

Both helpers take a connection (`conn *pgxpool.Conn`). The maintainer
holds the connection open for the parent's whole maintenance step, and
releases it after `Unlock`. This makes the advisory lock session-scoped
and safe against pool churn.

## Maintain semantics

`Maintain(ctx)` runs one full pass:

1. `Registry.ListParents(ctx)`. Log a tick-start line.
2. For each parent:
   - Skip if `AutomaticMaintenance` is false.
   - Acquire a pool connection.
   - Call `TryLock`. If `false`: log `INFO`, close the connection, and
     move on. If the lock succeeds:
     - Wrap the remaining work in a `defer` that recovers panics.
     - Call `Registry.ListTenants(ctx, ref)`.
     - If the parent has no `TenantColumn`: call
       `Provisioner.EnsurePartitions(ctx, parentRef, nil)`.
     - Else: iterate tenants and call
       `Provisioner.EnsurePartitions(ctx, parentRef, &tenantRef)` for
       each one.
     - Call `Retention.Sweep(ctx, parentRef)`.
     - Call `Unlock`.
   - Release the pool connection.
   - Emit a per-parent log line with counts.
3. Log a tick-end line with total counts.

Errors from one parent do not stop the loop. Errors are logged, and
the loop moves to the next parent. Panics are recovered and logged.

## Scheduler

`Start(ctx)` starts one goroutine:

- The goroutine creates `time.NewTicker(schedule)`.
- On each tick, the goroutine calls `Maintain(ctx)`. If the tick takes
  longer than the interval, the `time.Ticker` drops intervening ticks.
  The goroutine logs a warning when the tick drops.
- The goroutine watches a `done` channel. When `done` closes, the
  goroutine exits after the in-flight `Maintain(ctx)` returns.

`Stop(ctx)` closes `done` and waits on a `sync.WaitGroup`. If `ctx`
expires while the goroutine is still busy, `Stop` returns the
context's error, but the goroutine still cleans up.

`Start` is idempotent: a second call while running returns an error.
`Stop` on a not-started or already-stopped maintainer returns nil.

`Maintain` may be called without `Start`. Tests and one-off scripts
use this path.

## Manager wiring

`initInternals` constructs the maintainer after the registry:

```go
maint, err := maintainer.New(maintainer.Config{
    Pool:        m.db,
    Registry:    m.registry,
    Provisioner: m.provisioner,
    Retention:   m.retention,
    Clock:       m.clock,
    Logger:      m.logger,
    Schedule:    m.schedule,
})
if err != nil { return err }
m.maintainer = maint
```

`Manager.Start`, `Manager.Stop`, and `Manager.Maintain` delegate to the
field. The old `errors.ErrUnsupported` stubs go away.

## Retention import cycle

The retention package imports the registry package for
`ErrParentNotFound`. If the maintainer imports both the registry and
retention, there is no cycle. The maintainer package sits above them,
same as the manager.

## Unit tests

`maintainer_test.go` covers the pure logic. The tests use fakes for
the registry, provisioner, and retention. The tests do not touch a
database.

Cases:

- `Maintain_SkipsParentsWithAutomaticMaintenanceFalse`. Two parents;
  one has `AutomaticMaintenance=false`. Only the enabled parent goes
  through provisioner and retention.
- `Maintain_NoTenantColumn_CallsProvisionerWithNilTenant`. The parent
  has no `TenantColumn`. Provisioner is called once with a nil tenant
  pointer.
- `Maintain_TenantColumn_CallsProvisionerPerTenant`. The parent has
  two tenants. Provisioner is called twice with tenant refs.
- `Maintain_PanicInOneParent_ContinuesLoop`. The provisioner panics
  for parent A. Parent B still runs.
- `Maintain_ProvisionerErrorLogged_LoopContinues`. Provisioner returns
  an error for parent A. Retention still runs for parent B.
- `Start_StartsOnce_SecondCallErrors`. A second `Start` returns an
  error.
- `Stop_BeforeStart_ReturnsNil`. `Stop` on a fresh maintainer is
  harmless.
- `Stop_HonorsCtxDeadline`. A long-running Maintain blocks Stop; the
  ctx expires; Stop returns the ctx error; the loop still exits
  later.

Cases that touch a database go into the integration file.

## Integration tests

`integration_test.go` uses `testsupport.NewPG`. Build tag is
`//go:build integration`.

Cases:

- `Maintain_EndToEnd_ProvisionsAndSweeps`. Register a parent. Run
  `Maintain`. Verify one bounded child was created and one expired
  child was dropped.
- `Maintain_SkipsParentWhenLockHeldByOther`. Open a connection.
  Take the lock for `(schema, table)` out of band. Run `Maintain` in
  a second process (same DB). Verify the second process skipped the
  parent.
- `Maintain_TwoMaintainersOneDB_OnlyOneProcessesEachParent`. Two
  maintainers race the same DB. Only one call to `EnsurePartitions`
  runs per parent per tick. The other logs a lock-skip.
- `Start_Then_Stop_HonorsCtxDeadline`. Start a maintainer with a
  short schedule. Stop with a deadline that expires while a tick is
  running. Verify Stop returned within the deadline and the goroutine
  eventually exited.
- `Maintain_CalledDirectly_NoStart`. Do not call `Start`. Call
  `Maintain`. Verify partitions were created.

## Acceptance mapping

The plan meets every ADR 0007 acceptance point:

- Two processes maintain the same DB — covered by
  `Maintain_TwoMaintainersOneDB_OnlyOneProcessesEachParent`.
- `Stop(ctx)` returns before the deadline — covered by
  `Stop_HonorsCtxDeadline` (unit) and
  `Start_Then_Stop_HonorsCtxDeadline` (integration).
- Panic in one parent does not kill the loop — covered by
  `Maintain_PanicInOneParent_ContinuesLoop`.
- Skip on `AutomaticMaintenance=false` — covered by
  `Maintain_SkipsParentsWithAutomaticMaintenanceFalse`.
- Direct `Maintain` call without `Start` — covered by
  `Maintain_CalledDirectly_NoStart`.
- Simulated-clock retention window — covered by the retention step
  inside `Maintain_EndToEnd_ProvisionsAndSweeps`.

## Out of scope

- Meter, metrics, and metric names. ADR 0010 owns these. The plan
  keeps a comment where the metrics belong.
- Parallel per-parent goroutines. The plan runs sequentially, as the
  ADR calls out.
- Tick-report callback channel. The plan uses logs only.
