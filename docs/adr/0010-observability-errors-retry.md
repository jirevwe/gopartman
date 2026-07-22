# ADR 0010 — Observability, typed errors, retry policy

- **Epic**: E8
- **Status**: Accepted
- **Depends on**: 0001, 0004, 0005, 0006, 0007
- **Blocks**: 0011

## Context

Every prior epic returns `error`. Callers cannot branch on the error
today because the errors are opaque. Every prior epic emits log lines
via `slog.Default()`. Metrics are absent — the old library has a TODO
at `manager.go` line 18 that says "add metrics, lmao".

Prior epics also do NO retry on transient PostgreSQL errors. A
serialization failure aborts one full maintenance run. A brief
connection blip fails a whole tick.

This epic makes the observability surface real without changing call
sites in prior epics. The interfaces are new; the calls to them are
inserted at each site.

## Decision

- Public sentinels in root `errors.go`:
  ```go
  var (
      ErrParentNotFound         = errors.New("partman: parent not found")
      ErrTenantNotFound         = errors.New("partman: tenant not found")
      ErrParentAlreadyExists    = errors.New("partman: parent already exists")
      ErrTenantAlreadyExists    = errors.New("partman: tenant already exists")
      ErrTargetNotPartitioned   = errors.New("partman: target table is not partitioned by range")
      ErrColumnMissing          = errors.New("partman: required column missing on target")
      ErrParentNotTenanted      = errors.New("partman: parent has no tenant column")
      ErrArchiveSchemaMissing   = errors.New("partman: retention schema does not exist")
      ErrIntervalMismatch       = errors.New("partman: partition interval mismatch")
      ErrLockContention         = errors.New("partman: advisory lock held by another process")
      ErrHookVetoed             = errors.New("partman: pre-drop hook skipped the partition")
      ErrDefaultPartitionMissing = errors.New("partman: default partition missing")
      ErrInvalidIdentifier      = errors.New("partman: identifier contains invalid characters")
  )
  ```
- All internal errors wrap sentinels:
  ```go
  return fmt.Errorf("%w: parent=%s.%s", ErrParentNotFound, schema, table)
  ```
- Update Registry, Provisioner, Retention, Maintainer, Importer, and
  Drain to return wrapped sentinels. Callers use `errors.Is`.
- Public metrics interface in root `metrics.go`:
  ```go
  type Meter interface {
      Counter(name string, delta int64, tags ...string)
      Histogram(name string, value float64, tags ...string)
  }

  type NoopMeter struct{}

  func (NoopMeter) Counter(name string, delta int64, tags ...string)   {}
  func (NoopMeter) Histogram(name string, value float64, tags ...string) {}
  ```
- Ship `NoopMeter` as the default. Do NOT ship a Prometheus adapter
  in the root module. Ship it later in `partman/prometheus/` as a
  separate module.
- Tag convention: tags are alternating `key`, `value` strings. Never
  include `tenant_id` as a tag (high cardinality).
- Structured logging: `Manager` accepts `*slog.Logger` through
  `WithLogger(*slog.Logger)`. Default to `slog.Default()`. Every log
  line carries these keys where relevant: `parent`, `tenant`,
  `partition`, `duration_ms`, `err`.
- Retry package `internal/retry`:
  ```go
  type Policy struct {
      MaxAttempts int
      BaseDelay   time.Duration
      MaxDelay    time.Duration
      Jitter      float64
  }

  func Do(ctx context.Context, p Policy, fn func() error) error
  ```
  - Default policy: `MaxAttempts=5`, `BaseDelay=100ms`,
    `MaxDelay=2s`, `Jitter=0.2`.
  - Retries on:
    - `pgerrcode.SerializationFailure` (`40001`)
    - `pgerrcode.DeadlockDetected` (`40P01`)
    - `pgerrcode.ConnectionException` (`08000` family)
    - `net.Error` where `Timeout()` returns true.
  - Never retries on:
    - Constraint violations (`23xxx`).
    - Any `context.Canceled` or `context.DeadlineExceeded`.
    - Any wrapped sentinel above.
- Provisioner, Retention, and Drain call `retry.Do` per DDL statement
  or per batch. Registry does NOT retry — it is user-driven.
- Metric names (namespaced with `partman.`):
  - `partman.partitions_created_total`
  - `partman.default_partitions_created_total`
  - `partman.provisioner_duration_seconds`
  - `partman.partitions_dropped_total`
  - `partman.partitions_detached_total`
  - `partman.partitions_archived_total`
  - `partman.retention_skipped_total`
  - `partman.retention_duration_seconds`
  - `partman.maintenance_runs_total`
  - `partman.maintenance_duration_seconds`
  - `partman.lock_skipped_total`
  - `partman.parents_processed_total`
  - `partman.parents_panicked_total`
  - `partman.drain_rows_moved_total`
  - `partman.drain_batches_total`
  - `partman.drain_anomalies_total`
  - `partman.drain_duration_seconds`
  - `partman.retry_attempts_total`
  - `partman.retry_exhausted_total`

## Consequences

- Callers use `errors.Is(err, partman.ErrLockContention)` and branch.
- Metrics are opt-in. Zero cost by default (NoopMeter).
- Logging respects the standard library. Callers do not learn a new
  logging API.
- Retry is centralized. Prior epics call one function; the policy
  changes in one place.
- No `tenant_id` in metric tags. Operators use logs for per-tenant
  detail.

## Deliverables

- `errors.go` — public sentinels.
- `metrics.go` — `Meter`, `NoopMeter`.
- `internal/retry/retry.go` — retry loop and default policy.
- `internal/retry/retry_test.go` — retries on injected serialization
  failure and deadlock.
- Updates to `internal/registry`, `internal/provisioner`,
  `internal/retention`, `internal/maintainer`, `internal/importer`,
  `internal/drain`:
  - Wrap errors with sentinels.
  - Call `retry.Do` at every DDL boundary in provisioner, retention,
    drain.
  - Emit metrics at documented points.
  - Log with `slog` at the documented keys.
- Update `manager.go` to accept `WithMeter` and thread the meter into
  all internal packages.

## Acceptance

- `errors.Is(err, ErrParentNotFound)` is true for every not-found
  path in `Registry`.
- `retry.Do` succeeds on the second attempt when the first attempt
  returns `pgerrcode.SerializationFailure`.
- `retry.Do` exhausts and returns the underlying error after
  `MaxAttempts` deadlocks.
- `grep -R "time.Now()" .` returns only `clock.go`.
- `grep -R "log.Print" .` returns zero hits.
- `grep -R "fmt.Errorf" internal/` shows every non-`%w` occurrence is
  a legitimate ad-hoc format (no wrap needed).
- Running an integration test with a real Prometheus adapter (in a
  test file, not shipped) confirms every documented metric fires at
  least once during a full maintenance run.

## Open questions

- Include `tenant_id` as a tag? **Decision: no.** High cardinality
  kills Prometheus.
- Ship a Prometheus adapter now or later? **Decision: later**, in a
  separate module `partman/prometheus/`.
- Retry at each PG call or wrapped around whole methods? **Decision:
  each PG call.** Fine-grained.
- Should `WithMeter` accept multiple meters? **Decision: no.**
  Callers compose their own multi-meter if needed.
- Include a `TraceID` field on log lines? **Decision: rely on `slog`
  handlers to inject.** Not our job.

## References

- `CONTEXT.md` — no new terms.
- ADR-0001 — `WithMeter` and `WithLogger` options.
- ADR-0004, ADR-0006, ADR-0009 — points where `retry.Do` gets
  inserted.
- ADR-0007 — advisory lock skips emit `ErrLockContention`.
- pg_partman does not offer typed errors. This is a new-library
  addition.
