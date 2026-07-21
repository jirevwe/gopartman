# ADR 0001 — Public API skeleton and package layout

- **Epic**: E1a
- **Status**: Proposed
- **Depends on**: 0000
- **Blocks**: 0004, 0005, 0006, 0007

## Context

The repo has no public entry point. The root package exports only
`Clock`, `TableName`, `Bounds`, `Tenant`, and interval constants.
Epics E2, E3, E4, and E5 all need to code against stable Go signatures
in parallel. Without a locked API skeleton, each epic re-opens the
"what does the interface look like" conversation.

The old library at `/Users/rtukpe/Documents/dev/gopartman/manager.go`
is a 927-line god object. It mixes registry, provisioner, retention,
HTTP handlers, and scheduling. The user chose "Split by concern" to
avoid repeating that shape.

`types.go` today ships three dead helpers: `generatePartitionName`,
`buildTableName`, and the unexported `tableName` type. `TableName.Build`
supersedes all three. `types.go` also has a bug: `TableName.Interval`
is typed `time.Time` but the name suggests `time.Duration`.

`TableName.Build` produces names in one direction. Import and drain
epics (E6 and E7) need the inverse: `Parse`. Without it, each epic
invents its own regex.

## Decision

- Root package `go_partman` exposes the facade `Manager` and small
  helpers. No business logic in the root.
- Split behavior into internal packages under `internal/`:
  - `internal/registry` — parents and tenants lifecycle.
  - `internal/provisioner` — create partitions and the default.
  - `internal/retention` — drop, detach, archive.
  - `internal/maintainer` — the scheduler + advisory lock loop.
- Each internal package exposes ONE interface. `Manager` composes the
  four interfaces.
- Constructor: `New(opts ...Option) (*Manager, error)`. Required
  dependencies (`*pgxpool.Pool`, `Clock`) come through options. `New`
  returns a typed error when a required option is missing.
- Add `TableName.Parse(fqName string) (TableName, error)`. It is the
  inverse of `Build`. `Parse` and `Build` round-trip for every case in
  `table_test.go`.
- Fix `TableName.Interval` type: change `time.Time` to `time.Duration`.
- Delete legacy helpers from `types.go` in this epic:
  - `generatePartitionName`
  - `buildTableName`
  - the unexported `tableName` type
  - the unused `D` struct
- Define the `Hook` type in root `hook.go`:
  ```go
  type Hook func(ctx context.Context, ref PartitionRef) HookDecision

  type HookDecision int

  const (
      HookDrop HookDecision = iota
      HookDetach
      HookArchive
      HookSkip
  )
  ```
- Define `PartitionRef` in root `hook.go`:
  ```go
  type PartitionRef struct {
      Schema    string
      Parent    string
      TenantId  string
      Bounds    Bounds
      IsDefault bool
  }
  ```
- Fix `Manager`'s method surface (interfaces empty at this stage; all
  methods stub-return `errors.ErrUnsupported`):
  - `Start(ctx context.Context) error`
  - `Stop(ctx context.Context) error`
  - `Maintain(ctx context.Context) error`
  - `RegisterParent(ctx context.Context, p ParentConfig) error`
  - `RegisterTenant(ctx context.Context, t TenantConfig) error`
  - `RemoveParent(ctx context.Context, ref ParentRef, opts
    ...RemoveOption) error`
  - `RemoveTenant(ctx context.Context, ref TenantRef) error`
  - `ImportExisting(ctx context.Context, ref ParentRef)
    (ReconcileReport, error)`
  - `PartitionData(ctx context.Context, ref ParentRef, opts
    ...DrainOption) (DrainReport, error)`
- Options in `options.go`:
  - `WithDB(*pgxpool.Pool)` — required.
  - `WithClock(Clock)` — required.
  - `WithLogger(*slog.Logger)` — optional; defaults to
    `slog.Default()`.
  - `WithHook(Hook)` — optional; nil means "drop everything".
  - `WithScheduleInterval(time.Duration)` — optional; default `1h`.
  - `WithMeter(Meter)` — optional; defaults to no-op.

## Consequences

- Downstream epics run in parallel against stable signatures.
- The facade stays under 100 lines. All logic sits in internal
  packages.
- The legacy helpers cannot leak into new code.
- `TableName.Parse` becomes the single source of name parsing. E6 and
  E7 do not need their own regex.
- The `Manager` type is concrete (not an interface). Callers who need
  test doubles wrap it.

## Deliverables

- `manager.go` (facade with stubs)
- `options.go` (functional options)
- `hook.go` (`Hook`, `HookDecision`, `PartitionRef`)
- `internal/registry/registry.go` (interface + config types:
  `Registry`, `ParentConfig`, `TenantConfig`, `ParentRef`,
  `TenantRef`, `ParentInfo`, `TenantInfo`, `RemoveOption`)
- `internal/provisioner/provisioner.go` (interface: `Provisioner`)
- `internal/retention/retention.go` (interface: `Retention`,
  `SweepReport`)
- `internal/maintainer/maintainer.go` (interface: `Maintainer`)
- Update `table.go` — add `TableName.Parse`.
- Update `types.go`:
  - Delete `generatePartitionName`, `buildTableName`, `tableName`, `D`.
  - Change `TableName.Interval` field from `time.Time` to
    `time.Duration`.
- Update `table_test.go` — add `TestTableNameParse` covering every
  case that `TestTableNameGen` covers.

## Acceptance

- `go build ./...` passes.
- `go vet ./...` passes.
- `grep -R generatePartitionName ./` returns zero hits.
- `grep -R buildTableName ./` returns zero hits.
- `grep -R "type tableName" ./` returns zero hits.
- `grep -R "type D struct" ./` returns zero hits.
- `TestTableNameParse` covers all cases in `TestTableNameGen`.
- For every case, `Parse(Build(x)) == x`.
- The root package exports these names only: `Manager`, `New`,
  `Option`, `WithDB`, `WithClock`, `WithLogger`, `WithHook`,
  `WithScheduleInterval`, `WithMeter`, `Hook`, `HookDecision`,
  `HookDrop`, `HookDetach`, `HookArchive`, `HookSkip`, `PartitionRef`,
  `ParentConfig`, `TenantConfig`, `ParentRef`, `TenantRef`,
  `RemoveOption`, `WithCascadeDrop`, `ReconcileReport`,
  `DrainReport`, `DrainOption`, `Meter`, `NoopMeter`, `Clock`,
  `RealClock`, `SimulatedClock`, `NewRealClock`, `NewSimulatedClock`,
  `TableName`, `Bounds`, `Tenant`, `PartitionMonthInterval`,
  `PartitionWeekInterval`, `PartitionDayInterval`,
  `PartitionHourInterval`, `DateNoHyphens`, `Migrations`.

## Open questions

- `Manager` — interface or concrete? **Decision: concrete.** Callers
  wrap.
- `Hook` — one global hook or one per parent? **Decision: one
  global.** The hook filters by inspecting `PartitionRef.Parent`.
- `Stop()` — accept `context.Context`? **Decision: yes.** Shutdown
  needs a deadline.
- Should `Manager.RegisterParent` accept a per-call `Hook`, or is the
  global hook the only extension point? **Decision: global only for
  v1.** Simpler surface.
- Should `Options` return errors or panic on nil? **Decision: `New`
  returns the error.** Options are pure setters.

## References

- `CONTEXT.md` — glossary (Parent, Pre-Drop Hook, Bounds).
- Old library: `/Users/rtukpe/Documents/dev/gopartman/manager.go` —
  the god object we are avoiding.
- Old library: `/Users/rtukpe/Documents/dev/gopartman/type.go` — the
  functional-options pattern to keep, but with stricter validation.
