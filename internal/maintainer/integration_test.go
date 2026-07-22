//go:build integration

package maintainer_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oklog/ulid/v2"

	partman "github.com/jirevwe/go_partman"
	"github.com/jirevwe/go_partman/internal/maintainer"
	"github.com/jirevwe/go_partman/internal/provisioner"
	"github.com/jirevwe/go_partman/internal/registry"
	"github.com/jirevwe/go_partman/internal/retention"
	"github.com/jirevwe/go_partman/internal/testsupport"
)

// -----------------------------------------------------------------------------
// Fixtures
// -----------------------------------------------------------------------------

type parentFixture struct {
	Schema string
	Table  string
}

// createParent sets up a range-partitioned parent, registers it, and
// returns the schema/table pair. daily interval, 30-day retention,
// premake=1, automatic_maintenance=true.
func createParent(t *testing.T, pool *pgxpool.Pool, clock partman.Clock) parentFixture {
	t.Helper()
	ctx := t.Context()

	schema := "maint_" + strings.ToLower(ulid.Make().String())
	table := "events"
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+quoteIdent(schema)); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	ddl := fmt.Sprintf(
		`CREATE TABLE %s.%s (id BIGSERIAL, created_at TIMESTAMPTZ NOT NULL) PARTITION BY RANGE (created_at)`,
		quoteIdent(schema), quoteIdent(table),
	)
	if _, err := pool.Exec(ctx, ddl); err != nil {
		t.Fatalf("create parent table: %v", err)
	}

	reg := newRegistry(t, pool, clock)
	if err := reg.RegisterParent(ctx, registry.ParentConfig{
		SchemaName:        schema,
		TableName:         table,
		PartitionBy:       "created_at",
		PartitionInterval: 24 * time.Hour,
		Premake:           1,
		RetentionPeriod:   30 * 24 * time.Hour,
	}); err != nil {
		t.Fatalf("RegisterParent: %v", err)
	}
	return parentFixture{Schema: schema, Table: table}
}

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func newRegistry(t *testing.T, pool *pgxpool.Pool, clock partman.Clock) *registry.Impl {
	t.Helper()
	prov, err := provisioner.New(provisioner.Config{
		Pool:  pool,
		Clock: clock,
	})
	if err != nil {
		t.Fatalf("provisioner.New: %v", err)
	}
	ret, err := retention.New(retention.Config{
		Pool:  pool,
		Clock: clock,
	})
	if err != nil {
		t.Fatalf("retention.New: %v", err)
	}
	reg, err := registry.New(registry.Config{
		Pool:        pool,
		Provisioner: prov,
		Dropper:     retentionDropperAdapter{r: ret},
	})
	if err != nil {
		t.Fatalf("registry.New: %v", err)
	}
	return reg
}

type retentionDropperAdapter struct{ r *retention.Impl }

func (a retentionDropperAdapter) DropAll(ctx context.Context, ref registry.ParentRef) error {
	return a.r.DropAll(ctx, ref.SchemaName, ref.TableName)
}

// newMaintainer wires a Maintainer against the given clock.
func newMaintainer(t *testing.T, pool *pgxpool.Pool, clock partman.Clock, schedule time.Duration) *maintainer.Impl {
	t.Helper()
	prov, err := provisioner.New(provisioner.Config{Pool: pool, Clock: clock})
	if err != nil {
		t.Fatalf("provisioner.New: %v", err)
	}
	ret, err := retention.New(retention.Config{Pool: pool, Clock: clock})
	if err != nil {
		t.Fatalf("retention.New: %v", err)
	}
	reg, err := registry.New(registry.Config{
		Pool:        pool,
		Provisioner: prov,
		Dropper:     retentionDropperAdapter{r: ret},
	})
	if err != nil {
		t.Fatalf("registry.New: %v", err)
	}
	m, err := maintainer.New(maintainer.Config{
		Pool:        pool,
		Registry:    reg,
		Provisioner: prov,
		Retention:   ret,
		Clock:       clock,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Schedule:    schedule,
	})
	if err != nil {
		t.Fatalf("maintainer.New: %v", err)
	}
	return m
}

func countChildPartitions(t *testing.T, pool *pgxpool.Pool, schema, table string) int {
	t.Helper()
	var n int
	err := pool.QueryRow(t.Context(), `
		SELECT count(*)
		FROM pg_inherits i
		JOIN pg_class parent ON i.inhparent = parent.oid
		JOIN pg_namespace pn ON parent.relnamespace = pn.oid
		WHERE pn.nspname = $1 AND parent.relname = $2
	`, schema, table).Scan(&n)
	if err != nil {
		t.Fatalf("count children: %v", err)
	}
	return n
}

func tableExists(t *testing.T, pool *pgxpool.Pool, schema, table string) bool {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(t.Context(),
		`SELECT EXISTS (SELECT 1 FROM pg_tables WHERE schemaname = $1 AND tablename = $2)`,
		schema, table,
	).Scan(&exists); err != nil {
		t.Fatalf("check table exists: %v", err)
	}
	return exists
}

// -----------------------------------------------------------------------------
// Tests
// -----------------------------------------------------------------------------

func TestMaintain_EndToEnd_ProvisionsAndSweeps(t *testing.T) {
	pool, _ := testsupport.NewPG(t)

	// Register the parent at t=2026-04-01. Provisioner creates
	// _default + 2026-04-01 + 2026-04-02 (premake=1).
	regClock := partman.NewSimulatedClock(time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC))
	f := createParent(t, pool, regClock)

	// Advance the maintainer clock 60 days. The initial partition
	// (2026-04-01) is now more than 30 days past the cutoff and must
	// drop. Provisioner also makes today's partition and premake=1.
	maintClock := partman.NewSimulatedClock(time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC))
	m := newMaintainer(t, pool, maintClock, time.Hour)

	if err := m.Maintain(t.Context()); err != nil {
		t.Fatalf("Maintain: %v", err)
	}

	// The initial April 1 partition should be dropped.
	if tableExists(t, pool, f.Schema, "events_20260401") {
		t.Error("expected events_20260401 to be dropped by retention")
	}
	// The new current-day partition should exist.
	if !tableExists(t, pool, f.Schema, "events_20260601") {
		t.Error("expected events_20260601 to be created by provisioner")
	}
	// The default partition must never be touched.
	if !tableExists(t, pool, f.Schema, "events_default") {
		t.Error("default partition must always exist")
	}
	// Sanity: at least the default + current + premake children live.
	if got := countChildPartitions(t, pool, f.Schema, f.Table); got < 3 {
		t.Errorf("child count = %d, want at least 3", got)
	}
}

func TestMaintain_CalledDirectly_WithoutStart(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	clk := partman.NewSimulatedClock(time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC))
	f := createParent(t, pool, clk)

	m := newMaintainer(t, pool, clk, time.Hour)

	// No Start(). Direct Maintain() must still run a full pass.
	if err := m.Maintain(t.Context()); err != nil {
		t.Fatalf("Maintain: %v", err)
	}
	if !tableExists(t, pool, f.Schema, "events_default") {
		t.Error("default partition should exist after Maintain")
	}
}

func TestMaintain_TwoMaintainersOneDB_OnlyOneProcessesEachParent(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	clk := partman.NewSimulatedClock(time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC))
	f := createParent(t, pool, clk)

	// Take the advisory lock out of band from a dedicated connection.
	// This simulates the other maintainer holding the lock during a
	// tick.
	holder, err := pool.Acquire(t.Context())
	if err != nil {
		t.Fatalf("acquire holder conn: %v", err)
	}
	defer holder.Release()

	locked, err := maintainer.TryLock(t.Context(), holder, f.Schema, f.Table)
	if err != nil {
		t.Fatalf("TryLock holder: %v", err)
	}
	if !locked {
		t.Fatal("holder should have taken the lock")
	}

	// Snapshot partition count so we can prove the second maintainer
	// did nothing.
	before := countChildPartitions(t, pool, f.Schema, f.Table)

	// Advance the clock so a fresh Maintain would create a new day.
	clk.SetTime(time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC))

	m := newMaintainer(t, pool, clk, time.Hour)
	if err := m.Maintain(t.Context()); err != nil {
		t.Fatalf("Maintain: %v", err)
	}

	after := countChildPartitions(t, pool, f.Schema, f.Table)
	if before != after {
		t.Errorf("second maintainer created %d partitions; expected 0 (lock held)", after-before)
	}

	// Release the holder. The next Maintain call now succeeds and
	// creates the new partitions.
	if err := maintainer.Unlock(t.Context(), holder, f.Schema, f.Table); err != nil {
		t.Fatalf("Unlock holder: %v", err)
	}

	if err := m.Maintain(t.Context()); err != nil {
		t.Fatalf("second Maintain: %v", err)
	}
	if got := countChildPartitions(t, pool, f.Schema, f.Table); got <= after {
		t.Errorf("after releasing the lock, expected new partitions; before=%d after=%d", after, got)
	}
}

// TestMaintain_EmitsAllDocumentedMetrics wires a captureMeter through
// registry, provisioner, retention, and maintainer. It runs one full
// maintenance pass over a parent whose registration created bounded
// partitions past retention, forcing every path to fire at least once:
// provisioner (create + duration), retention (drop + duration), and
// maintainer (run + duration + processed).
func TestMaintain_EmitsAllDocumentedMetrics(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	meter := testsupport.NewCaptureMeter()

	regClock := partman.NewSimulatedClock(time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC))
	f := createParentWithMeter(t, pool, regClock, meter)

	// Move to a time where the initial 2026-04-01 partition is past
	// the 30-day retention cutoff.
	maintClock := partman.NewSimulatedClock(time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC))
	m := newMaintainerWithMeter(t, pool, maintClock, time.Hour, meter)

	if err := m.Maintain(t.Context()); err != nil {
		t.Fatalf("Maintain: %v", err)
	}

	want := []string{
		"partman.partitions_created_total",
		"partman.default_partitions_created_total",
		"partman.provisioner_duration_seconds",
		"partman.partitions_dropped_total",
		"partman.retention_duration_seconds",
		"partman.maintenance_runs_total",
		"partman.maintenance_duration_seconds",
		"partman.parents_processed_total",
	}
	for _, name := range want {
		if !meter.Fired(name) {
			t.Errorf("metric %s did not fire", name)
		}
	}
	_ = f
}

func createParentWithMeter(t *testing.T, pool *pgxpool.Pool, clock partman.Clock, meter *testsupport.CaptureMeter) parentFixture {
	t.Helper()
	ctx := t.Context()

	schema := "maint_" + strings.ToLower(ulid.Make().String())
	table := "events"
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+quoteIdent(schema)); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	ddl := fmt.Sprintf(
		`CREATE TABLE %s.%s (id BIGSERIAL, created_at TIMESTAMPTZ NOT NULL) PARTITION BY RANGE (created_at)`,
		quoteIdent(schema), quoteIdent(table),
	)
	if _, err := pool.Exec(ctx, ddl); err != nil {
		t.Fatalf("create parent table: %v", err)
	}

	prov, err := provisioner.New(provisioner.Config{Pool: pool, Clock: clock, Meter: meter})
	if err != nil {
		t.Fatalf("provisioner.New: %v", err)
	}
	ret, err := retention.New(retention.Config{Pool: pool, Clock: clock, Meter: meter})
	if err != nil {
		t.Fatalf("retention.New: %v", err)
	}
	reg, err := registry.New(registry.Config{
		Pool:        pool,
		Provisioner: prov,
		Dropper:     retentionDropperAdapter{r: ret},
	})
	if err != nil {
		t.Fatalf("registry.New: %v", err)
	}
	if err := reg.RegisterParent(ctx, registry.ParentConfig{
		SchemaName:        schema,
		TableName:         table,
		PartitionBy:       "created_at",
		PartitionInterval: 24 * time.Hour,
		Premake:           1,
		RetentionPeriod:   30 * 24 * time.Hour,
	}); err != nil {
		t.Fatalf("RegisterParent: %v", err)
	}
	return parentFixture{Schema: schema, Table: table}
}

func newMaintainerWithMeter(t *testing.T, pool *pgxpool.Pool, clock partman.Clock, schedule time.Duration, meter *testsupport.CaptureMeter) *maintainer.Impl {
	t.Helper()
	prov, err := provisioner.New(provisioner.Config{Pool: pool, Clock: clock, Meter: meter})
	if err != nil {
		t.Fatalf("provisioner.New: %v", err)
	}
	ret, err := retention.New(retention.Config{Pool: pool, Clock: clock, Meter: meter})
	if err != nil {
		t.Fatalf("retention.New: %v", err)
	}
	reg, err := registry.New(registry.Config{
		Pool:        pool,
		Provisioner: prov,
		Dropper:     retentionDropperAdapter{r: ret},
	})
	if err != nil {
		t.Fatalf("registry.New: %v", err)
	}
	m, err := maintainer.New(maintainer.Config{
		Pool:        pool,
		Registry:    reg,
		Provisioner: prov,
		Retention:   ret,
		Clock:       clock,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Meter:       meter,
		Schedule:    schedule,
	})
	if err != nil {
		t.Fatalf("maintainer.New: %v", err)
	}
	return m
}

func TestStart_Then_Stop_HonorsCtxDeadline(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	clk := partman.NewSimulatedClock(time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC))
	// No parent; ticks are essentially no-ops. This test verifies the
	// scheduler lifecycle, not the maintenance work.
	m := newMaintainer(t, pool, clk, 20*time.Millisecond)

	if err := m.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Let a few ticks fire.
	time.Sleep(80 * time.Millisecond)

	stopCtx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := m.Stop(stopCtx); err != nil {
		t.Errorf("Stop returned %v, want nil (idle loop finishes fast)", err)
	}
	if elapsed := time.Since(start); elapsed > 400*time.Millisecond {
		t.Errorf("Stop took %s, want under 400ms", elapsed)
	}
}

func TestStart_TwoMaintainersInProcess_RaceForLock(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	clk := partman.NewSimulatedClock(time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC))
	f := createParent(t, pool, clk)

	m1 := newMaintainer(t, pool, clk, 30*time.Millisecond)
	m2 := newMaintainer(t, pool, clk, 30*time.Millisecond)

	if err := m1.Start(t.Context()); err != nil {
		t.Fatalf("Start m1: %v", err)
	}
	if err := m2.Start(t.Context()); err != nil {
		t.Fatalf("Start m2: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = m1.Stop(ctx)
		_ = m2.Stop(ctx)
	}()

	// Let several ticks race. Both maintainers try to Maintain; the
	// advisory lock keeps them from conflicting DDL. If either
	// panicked or corrupted state, tableExists below would fail.
	time.Sleep(200 * time.Millisecond)

	if !tableExists(t, pool, f.Schema, "events_default") {
		t.Error("default partition should exist after concurrent ticks")
	}
	// The parent is still healthy: querying its partition metadata
	// should still succeed and be consistent.
	if got := countChildPartitions(t, pool, f.Schema, f.Table); got < 2 {
		t.Errorf("child count = %d, want at least 2 after concurrent maintenance", got)
	}
}
