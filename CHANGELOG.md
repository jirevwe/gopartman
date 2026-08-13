# Changelog

All notable changes to `gopartman` are recorded in this file.

The format is loosely based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). This project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

Name parsing against the parent (ADR-0012):

- `TableName.Parse` takes the parent table name as a second argument:
  `Parse(fqName, parentName string)`. A tenant segment is otherwise
  indistinguishable from the last word of a multi-word parent — in
  `public.order_items_20060102`, `items` could be either, and no property
  of the string decides which. Callers always know the parent they are
  reconciling. Breaking for direct callers of `Parse`; `Build` is
  unchanged.

### Fixed

- `ImportExisting` adopts partitions whose name carries a lower-case tenant
  segment. PostgreSQL folds unquoted identifiers, so a partition created by
  DDL outside this package could not be parsed back into a tenant. It was
  reported as drift and never written to `partman.partitions`, which left
  it outside retention's view: the table grew without bound while retention
  ran to completion and reported success.

## [0.1.0] — Unreleased

Initial public release. First release-tag candidate.

### Added

Public API skeleton (E1a, ADR-0001):

- Facade type `Manager` with `Start`, `Stop`, `Maintain`,
  `RegisterParent`, `RegisterTenant`, `RemoveParent`, `RemoveTenant`,
  `ListParents`, `ListTenants`, `ImportExisting`, `PartitionData`.
- Constructor `New(opts ...Option)` with functional options
  `WithDB`, `WithClock`, `WithLogger`, `WithHook`,
  `WithScheduleInterval`, `WithMeter`.
- `Clock` interface, `RealClock`, `SimulatedClock`.
- `TableName` with `Build`/`Parse`, plus `Bounds`, `Tenant`, and
  the four `PartitionXInterval` sentinel constants.

Metadata schema (E1b, ADR-0002):

- Embedded migrations exposed via `Migrations()`.
- `partman.parent_tables`, `partman.tenants`, `partman.partitions`
  with an `is_default` unique index and a tenant-validation trigger.

Integration test harness (E1c, ADR-0003):

- `internal/testsupport.NewPG` — shared `postgres:16-alpine`
  container per test binary, driven by testcontainers-go.

Provisioner (E2, ADR-0004):

- Creates bounded partitions and the default partition for
  registered parents. Idempotent under repeat calls.

Registry (E3, ADR-0005):

- `RegisterParent` validates the parent table and provisions
  initial partitions.
- `RegisterTenant` provisions per-tenant partitions when the parent
  has a `TenantColumn`.
- `RemoveParent` / `RemoveTenant` with `WithCascadeDrop` option for
  dropping child tables on removal.

Retention (E4, ADR-0006):

- `Sweep` scans registered parents and drops, detaches, or
  archives expired partitions.
- Global `Hook` type (`HookDrop`, `HookDetach`, `HookArchive`,
  `HookSkip`) decides fate per candidate.
- `WithDryRun` reports without applying DDL.
- Default partition is never dropped.

Maintainer (E5, ADR-0007):

- Scheduler with `Start` / `Stop` and a per-parent PostgreSQL
  advisory lock. `Maintain` triggers one on-demand run.

Import & reconcile (E6, ADR-0008):

- `ImportExisting` reads existing PG children and inserts missing
  metadata rows. Returns a `ReconcileReport` with `Imported`,
  `Drifted`, `Orphaned`, `Skipped` entries.

`partition_data` drain (E7, ADR-0009):

- `PartitionData` moves rows out of the default partition into the
  correct bounded children in batches. Options: `WithBatchSize`,
  `WithMaxBatches`, `WithTenant`. `DrainAnomaly` records rows the
  drain could not place.

Observability, typed errors, retry (E8, ADR-0010):

- `Meter` interface (`NoopMeter` default) with counters and
  histograms for partitions created, dropped, drained, and for
  maintenance-run latency.
- Sentinel errors centralized in the `errs` package.
- Retry policies for transient errors around provisioning and
  retention.

Docs (E9, ADR-0011):

- Rewritten `README.md`.
- Package doc comment in `doc.go` so `go doc` renders a summary.
- Runnable programs: `examples/basic`, `examples/multitenant`.
- `docs/architecture.md` with an ASCII composition diagram.
- Glossary expanded in `CONTEXT.md` to cover every exported name in
  the root package.
- ADR statuses flipped to `Implemented`.

### Changed

Nothing yet — this is the first release.

### Deprecated

Nothing.

### Removed

Nothing.

### Fixed

Nothing.

### Security

Nothing.

[Unreleased]: https://github.com/jirevwe/gopartman/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/jirevwe/gopartman/releases/tag/v0.1.0
