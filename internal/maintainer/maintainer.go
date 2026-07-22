// Package maintainer runs the periodic maintenance loop and holds the
// per-parent advisory lock. ADR-0007 defines the semantics implemented
// here.
//
// The maintainer sits above Registry, Provisioner, and Retention. It
// polls the registry for parents, takes a per-parent advisory lock so
// two replicas do not step on each other, and delegates the actual
// work to Provisioner and Retention. The maintainer holds no locks
// itself; it defers to PostgreSQL for coordination.
package maintainer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jirevwe/gopartman/internal/errs"
	"github.com/jirevwe/gopartman/internal/provisioner"
	"github.com/jirevwe/gopartman/internal/registry"
	"github.com/jirevwe/gopartman/internal/retention"
)

// Maintainer is the seam through which Manager runs the maintenance
// loop. It exposes Start, Stop, and Maintain. Callers pick Start for
// the long-running loop or Maintain for a one-shot pass.
type Maintainer interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Maintain(ctx context.Context) error
}

// ParentLister is the narrow slice of Registry the maintainer needs.
// registry.Impl satisfies it structurally.
type ParentLister interface {
	ListParents(ctx context.Context) ([]registry.ParentInfo, error)
	ListTenants(ctx context.Context, ref registry.ParentRef) ([]registry.TenantInfo, error)
}

// PartitionEnsurer is the narrow slice of Provisioner the maintainer
// needs. provisioner.Impl satisfies it structurally.
type PartitionEnsurer interface {
	EnsurePartitions(ctx context.Context, parent provisioner.ParentRef, tenant *provisioner.TenantRef) (provisioner.EnsureReport, error)
}

// RetentionSweeper is the narrow slice of Retention the maintainer
// needs. retention.Impl satisfies it structurally.
type RetentionSweeper interface {
	Sweep(ctx context.Context, parent retention.ParentRef, opts ...retention.SweepOption) (retention.SweepReport, error)
}

// Clock reads wall-clock time. The maintainer uses it only for log
// timings; the ticker uses time.Ticker directly.
type Clock interface {
	Now() time.Time
}

// Meter is the observability sink Maintainer needs. The root
// partman.Meter satisfies it. A nil Meter turns emission off.
type Meter interface {
	Counter(name string, delta int64, tags ...string)
	Histogram(name string, value float64, tags ...string)
}

type noopMeter struct{}

func (noopMeter) Counter(name string, delta int64, tags ...string)     {}
func (noopMeter) Histogram(name string, value float64, tags ...string) {}

// Locker guards one parent's maintenance step against concurrent
// maintainers. The default implementation uses PostgreSQL session-
// scoped advisory locks. Tests inject a fake Locker to drive the loop
// without a real database.
//
// TryLock returns (locked=true, release, nil) when the caller now
// holds the lock. It returns (locked=false, nil, nil) when another
// session holds it. It returns (false, nil, err) on infrastructure
// failure. The release function is safe to call once; it releases the
// lock and any underlying resources.
type Locker interface {
	TryLock(ctx context.Context, schema, table string) (locked bool, release func(), err error)
}

// Config bundles the dependencies for constructing an Impl.
type Config struct {
	Pool        *pgxpool.Pool
	Registry    ParentLister
	Provisioner PartitionEnsurer
	Retention   RetentionSweeper
	Clock       Clock
	Logger      *slog.Logger
	Meter       Meter
	// Schedule sets the interval between ticks. Zero means "1 hour",
	// matching the ADR-0007 default.
	Schedule time.Duration
	// Locker overrides the default advisory-lock implementation. Zero
	// value means "use pg_try_advisory_lock via the pool".
	Locker Locker
}

// Impl is the concrete Maintainer. Exported so the Manager facade can
// hold a typed field.
type Impl struct {
	pool        *pgxpool.Pool
	registry    ParentLister
	provisioner PartitionEnsurer
	retention   RetentionSweeper
	clock       Clock
	logger      *slog.Logger
	meter       Meter
	schedule    time.Duration
	locker      Locker

	sched *scheduler
}

const defaultSchedule = time.Hour

// New constructs an Impl. Pool, Registry, Provisioner, Retention, and
// Clock are required. Schedule defaults to 1h. Logger defaults to
// slog.Default().
func New(cfg Config) (*Impl, error) {
	if cfg.Registry == nil {
		return nil, errors.New("maintainer: Config.Registry is required")
	}
	if cfg.Provisioner == nil {
		return nil, errors.New("maintainer: Config.Provisioner is required")
	}
	if cfg.Retention == nil {
		return nil, errors.New("maintainer: Config.Retention is required")
	}
	if cfg.Clock == nil {
		return nil, errors.New("maintainer: Config.Clock is required")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	meter := cfg.Meter
	if meter == nil {
		meter = noopMeter{}
	}
	schedule := cfg.Schedule
	if schedule <= 0 {
		schedule = defaultSchedule
	}
	locker := cfg.Locker
	if locker == nil {
		if cfg.Pool == nil {
			return nil, errors.New("maintainer: Config.Pool is required when Config.Locker is nil")
		}
		locker = &poolLocker{pool: cfg.Pool, logger: logger}
	}
	m := &Impl{
		pool:        cfg.Pool,
		registry:    cfg.Registry,
		provisioner: cfg.Provisioner,
		retention:   cfg.Retention,
		clock:       cfg.Clock,
		logger:      logger,
		meter:       meter,
		schedule:    schedule,
		locker:      locker,
	}
	m.sched = newScheduler(m)
	return m, nil
}

// Start begins the maintenance loop. It returns an error if the loop
// is already running.
func (m *Impl) Start(ctx context.Context) error {
	return m.sched.start(ctx)
}

// Stop halts the maintenance loop and waits for the in-flight tick to
// finish. Stop returns the ctx error when ctx expires before the
// goroutine exits; the goroutine still cleans up on its own.
func (m *Impl) Stop(ctx context.Context) error {
	return m.sched.stop(ctx)
}

// Maintain runs one maintenance pass over every registered parent.
// The caller may invoke it directly without Start; tests and one-off
// scripts use this path.
//
// A ctx cancellation stops the pass at the next parent boundary. A
// panic inside one parent's work is recovered and logged; the loop
// moves on. A retention or provisioner error is logged; the loop
// moves on.
func (m *Impl) Maintain(ctx context.Context) error {
	tickID := m.clock.Now().UTC().Format("20060102T150405.000000")
	parents, err := m.registry.ListParents(ctx)
	if err != nil {
		return fmt.Errorf("maintainer: list parents: %w", err)
	}
	m.logger.Info("maintainer: tick start",
		"tick_id", tickID,
		"parent_count", len(parents),
	)
	tickStart := m.clock.Now()
	skipped := 0
	processed := 0
	panicked := 0

	for _, p := range parents {
		select {
		case <-ctx.Done():
			m.logger.Info("maintainer: tick canceled",
				"tick_id", tickID,
				"err", ctx.Err(),
			)
			return ctx.Err()
		default:
		}
		if !p.AutomaticMaintenance {
			m.logger.Debug("maintainer: parent has automatic_maintenance=false; skipping",
				"parent", parentLabel(p),
			)
			continue
		}
		result := m.processParent(ctx, p)
		switch result {
		case parentResultLocked:
			skipped++
		case parentResultProcessed:
			processed++
		case parentResultPanicked:
			panicked++
		}
	}

	tickDuration := m.clock.Now().Sub(tickStart)
	m.logger.Info("maintainer: tick end",
		"tick_id", tickID,
		"processed_count", processed,
		"skipped_count", skipped,
		"panicked_count", panicked,
		"duration_ms", tickDuration.Milliseconds(),
	)
	m.meter.Counter("partman.maintenance_runs_total", 1)
	m.meter.Histogram("partman.maintenance_duration_seconds", tickDuration.Seconds())
	return nil
}

// parentResult labels the outcome of one parent's step. It is used
// only for counters in the tick-end log line.
type parentResult int

const (
	parentResultProcessed parentResult = iota
	parentResultLocked
	parentResultPanicked
)

// processParent runs one parent's maintenance step. It acquires the
// advisory lock, calls Provisioner and Retention, and releases the
// lock. Panics inside the step are recovered and logged; they do NOT
// escape.
func (m *Impl) processParent(ctx context.Context, p registry.ParentInfo) (result parentResult) {
	label := parentLabel(p)
	parentStart := m.clock.Now()

	defer func() {
		if r := recover(); r != nil {
			m.logger.Error("maintainer: panic while processing parent",
				"parent", label,
				"panic", fmt.Sprintf("%v", r),
			)
			m.meter.Counter("partman.parents_panicked_total", 1, "parent", label)
			result = parentResultPanicked
		}
	}()

	locked, release, err := m.locker.TryLock(ctx, p.SchemaName, p.TableName)
	if err != nil {
		m.logger.Warn("maintainer: TryLock failed; skipping parent",
			"parent", label,
			"err", err,
		)
		return parentResultLocked
	}
	if !locked {
		m.logger.Info("maintainer: another session holds the advisory lock; skipping",
			"parent", label,
			"err", errs.ErrLockContention,
		)
		m.meter.Counter("partman.lock_skipped_total", 1, "parent", label)
		return parentResultLocked
	}
	defer release()

	partitionsCreated := 0
	defaultCreated := false
	if p.TenantColumn == "" {
		rep, err := m.provisioner.EnsurePartitions(ctx,
			provisioner.ParentRef{SchemaName: p.SchemaName, TableName: p.TableName},
			nil,
		)
		if err != nil {
			m.logger.Warn("maintainer: EnsurePartitions failed",
				"parent", label,
				"err", err,
			)
		} else {
			partitionsCreated += rep.BoundedCreated
			if rep.DefaultCreated {
				defaultCreated = true
			}
		}
	} else {
		tenants, err := m.registry.ListTenants(ctx,
			registry.ParentRef{SchemaName: p.SchemaName, TableName: p.TableName},
		)
		if err != nil {
			m.logger.Warn("maintainer: ListTenants failed",
				"parent", label,
				"err", err,
			)
		} else {
			for _, tn := range tenants {
				rep, err := m.provisioner.EnsurePartitions(ctx,
					provisioner.ParentRef{SchemaName: p.SchemaName, TableName: p.TableName},
					&provisioner.TenantRef{TenantId: tn.TenantId},
				)
				if err != nil {
					m.logger.Warn("maintainer: EnsurePartitions failed for tenant",
						"parent", label,
						"tenant", tn.TenantId,
						"err", err,
					)
					continue
				}
				partitionsCreated += rep.BoundedCreated
				if rep.DefaultCreated {
					defaultCreated = true
				}
			}
		}
	}

	sweep, err := m.retention.Sweep(ctx,
		retention.ParentRef{SchemaName: p.SchemaName, TableName: p.TableName},
	)
	if err != nil {
		m.logger.Warn("maintainer: Retention.Sweep failed",
			"parent", label,
			"err", err,
		)
	}

	m.logger.Info("maintainer: parent processed",
		"parent", label,
		"duration_ms", m.clock.Now().Sub(parentStart).Milliseconds(),
		"partitions_created", partitionsCreated,
		"default_created", defaultCreated,
		"partitions_dropped", len(sweep.Dropped),
		"partitions_detached", len(sweep.Detached),
		"partitions_archived", len(sweep.Archived),
	)
	m.meter.Counter("partman.parents_processed_total", 1, "parent", label)
	return parentResultProcessed
}

func parentLabel(p registry.ParentInfo) string {
	return p.SchemaName + "." + p.TableName
}
