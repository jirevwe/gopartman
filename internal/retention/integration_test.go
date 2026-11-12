//go:build integration

package retention_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oklog/ulid/v2"

	partman "github.com/jirevwe/gopartman"
	"github.com/jirevwe/gopartman/internal/hooks"
	"github.com/jirevwe/gopartman/internal/retention"
	"github.com/jirevwe/gopartman/internal/testsupport"
)

// -----------------------------------------------------------------------------
// Fixtures
// -----------------------------------------------------------------------------

type parentFixture struct {
	Schema          string
	Table           string
	ParentID        string
	RetentionSchema string
}

type parentOpt func(*parentOpts)

type parentOpts struct {
	retentionSchema string
	tenantColumn    string
}

func withRetentionSchema(s string) parentOpt { return func(o *parentOpts) { o.retentionSchema = s } }
func withTenantColumn(c string) parentOpt    { return func(o *parentOpts) { o.tenantColumn = c } }

// setupParent creates a fresh schema, a range-partitioned parent
// table, and the corresponding partman.parent_tables row with a 30-
// day retention_period. Returns fixture details for further inserts.
func setupParent(t *testing.T, pool *pgxpool.Pool, opts ...parentOpt) parentFixture {
	t.Helper()
	ctx := t.Context()

	o := parentOpts{}
	for _, opt := range opts {
		opt(&o)
	}

	schema := "ret_" + strings.ToLower(ulid.Make().String())
	table := "events"
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+quoteIdent(schema)); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	var partitionBy, createDDL string
	var tenantColArg any
	if o.tenantColumn == "" {
		partitionBy = "created_at"
		createDDL = fmt.Sprintf(
			`CREATE TABLE %s.%s (id BIGSERIAL, created_at TIMESTAMPTZ NOT NULL) PARTITION BY RANGE (created_at)`,
			quoteIdent(schema), quoteIdent(table),
		)
	} else {
		partitionBy = o.tenantColumn + ", created_at"
		tenantColArg = o.tenantColumn
		createDDL = fmt.Sprintf(
			`CREATE TABLE %s.%s (id BIGSERIAL, %s VARCHAR NOT NULL, created_at TIMESTAMPTZ NOT NULL) PARTITION BY RANGE (%s, created_at)`,
			quoteIdent(schema), quoteIdent(table), quoteIdent(o.tenantColumn), quoteIdent(o.tenantColumn),
		)
	}
	if _, err := pool.Exec(ctx, createDDL); err != nil {
		t.Fatalf("create parent: %v", err)
	}

	if o.retentionSchema != "" {
		if _, err := pool.Exec(ctx, "CREATE SCHEMA "+quoteIdent(o.retentionSchema)); err != nil {
			t.Fatalf("create retention schema: %v", err)
		}
	}

	parentID := ulid.Make().String()
	var retentionSchemaArg any
	if o.retentionSchema != "" {
		retentionSchemaArg = o.retentionSchema
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO partman.parent_tables
			(id, schema_name, table_name, tenant_column,
			 partition_by, partition_type, partition_interval,
			 retention_period, retention_keep_table, retention_schema,
			 automatic_maintenance, premake)
		VALUES ($1, $2, $3, $4, $5, 'range', 'daily',
			INTERVAL '30 days', false, $6, true, 1)
	`, parentID, schema, table, tenantColArg, partitionBy, retentionSchemaArg)
	if err != nil {
		t.Fatalf("insert parent_tables row: %v", err)
	}

	return parentFixture{
		Schema:          schema,
		Table:           table,
		ParentID:        parentID,
		RetentionSchema: o.retentionSchema,
	}
}

// insertBoundedPartition creates a CREATE TABLE ... PARTITION OF child
// and inserts the matching partman.partitions row. Name is the
// child's local table name (no schema); the FQ form goes to metadata.
func insertBoundedPartition(t *testing.T, pool *pgxpool.Pool, f parentFixture, childTable string, from, to time.Time) string {
	t.Helper()
	ctx := t.Context()

	ddl := fmt.Sprintf(
		`CREATE TABLE %s.%s PARTITION OF %s.%s FOR VALUES FROM (TIMESTAMPTZ '%s') TO (TIMESTAMPTZ '%s')`,
		quoteIdent(f.Schema), quoteIdent(childTable),
		quoteIdent(f.Schema), quoteIdent(f.Table),
		from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339),
	)
	if _, err := pool.Exec(ctx, ddl); err != nil {
		t.Fatalf("create child %s: %v", childTable, err)
	}

	partitionID := ulid.Make().String()
	fq := f.Schema + "." + childTable
	if _, err := pool.Exec(ctx, `
		INSERT INTO partman.partitions
			(id, name, parent_table_id, tenant_id,
			 partition_by, partition_type,
			 partition_bounds_from, partition_bounds_to,
			 is_default, status)
		VALUES ($1, $2, $3, NULL, 'created_at', 'range', $4, $5, false, 'active')
	`, partitionID, fq, f.ParentID, from, to); err != nil {
		t.Fatalf("insert partitions row: %v", err)
	}
	return partitionID
}

func insertDefaultPartition(t *testing.T, pool *pgxpool.Pool, f parentFixture) string {
	t.Helper()
	ctx := t.Context()

	childTable := f.Table + "_default"
	ddl := fmt.Sprintf(
		`CREATE TABLE %s.%s PARTITION OF %s.%s DEFAULT`,
		quoteIdent(f.Schema), quoteIdent(childTable),
		quoteIdent(f.Schema), quoteIdent(f.Table),
	)
	if _, err := pool.Exec(ctx, ddl); err != nil {
		t.Fatalf("create default child: %v", err)
	}

	partitionID := ulid.Make().String()
	fq := f.Schema + "." + childTable
	epoch := time.Time{}
	if _, err := pool.Exec(ctx, `
		INSERT INTO partman.partitions
			(id, name, parent_table_id, tenant_id,
			 partition_by, partition_type,
			 partition_bounds_from, partition_bounds_to,
			 is_default, status)
		VALUES ($1, $2, $3, NULL, 'created_at', 'range', $4, $4, true, 'active')
	`, partitionID, fq, f.ParentID, epoch); err != nil {
		t.Fatalf("insert default partitions row: %v", err)
	}
	return partitionID
}

func ensureTenant(t *testing.T, pool *pgxpool.Pool, parentID, tenantID string) {
	t.Helper()
	_, err := pool.Exec(t.Context(),
		`INSERT INTO partman.tenants (id, parent_table_id) VALUES ($1, $2)
		 ON CONFLICT DO NOTHING`,
		tenantID, parentID,
	)
	if err != nil {
		t.Fatalf("insert tenant %s: %v", tenantID, err)
	}
}

// insertTenantPartition creates a composite-range child for a tenanted
// parent. Child names follow {parent}_{tenant}_{YYYYMMDD}.
func insertTenantPartition(t *testing.T, pool *pgxpool.Pool, f parentFixture, childTable, tenantID string, from, to time.Time) string {
	t.Helper()
	ctx := t.Context()
	ensureTenant(t, pool, f.ParentID, tenantID)

	ddl := fmt.Sprintf(
		`CREATE TABLE %s.%s PARTITION OF %s.%s FOR VALUES FROM ('%s', TIMESTAMPTZ '%s') TO ('%s', TIMESTAMPTZ '%s')`,
		quoteIdent(f.Schema), quoteIdent(childTable),
		quoteIdent(f.Schema), quoteIdent(f.Table),
		tenantID, from.UTC().Format(time.RFC3339),
		tenantID, to.UTC().Format(time.RFC3339),
	)
	if _, err := pool.Exec(ctx, ddl); err != nil {
		t.Fatalf("create tenant child %s: %v", childTable, err)
	}

	partitionID := ulid.Make().String()
	fq := f.Schema + "." + childTable
	if _, err := pool.Exec(ctx, `
		INSERT INTO partman.partitions
			(id, name, parent_table_id, tenant_id,
			 partition_by, partition_type,
			 partition_bounds_from, partition_bounds_to,
			 is_default, status)
		VALUES ($1, $2, $3, $4, 'project_id, created_at', 'range', $5, $6, false, 'active')
	`, partitionID, fq, f.ParentID, tenantID, from, to); err != nil {
		t.Fatalf("insert tenant partitions row: %v", err)
	}
	return partitionID
}

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func newRetention(t *testing.T, pool *pgxpool.Pool, clockAt time.Time, hook hooks.Hook) *retention.Impl {
	t.Helper()
	return newRetentionMode(t, pool, clockAt, hook, false)
}

func newRetentionMode(t *testing.T, pool *pgxpool.Pool, clockAt time.Time, hook hooks.Hook, online bool) *retention.Impl {
	t.Helper()
	r, err := retention.New(retention.Config{
		Pool:   pool,
		Clock:  partman.NewSimulatedClock(clockAt),
		Hook:   hook,
		Online: online,
	})
	if err != nil {
		t.Fatalf("retention.New: %v", err)
	}
	return r
}

func tableExists(t *testing.T, pool *pgxpool.Pool, schema, table string) bool {
	t.Helper()
	var exists bool
	err := pool.QueryRow(t.Context(),
		`SELECT EXISTS (SELECT 1 FROM pg_tables WHERE schemaname = $1 AND tablename = $2)`,
		schema, table,
	).Scan(&exists)
	if err != nil {
		t.Fatalf("check table exists: %v", err)
	}
	return exists
}

func isChildOf(t *testing.T, pool *pgxpool.Pool, parentSchema, parentTable, childSchema, childTable string) bool {
	t.Helper()
	var n int
	err := pool.QueryRow(t.Context(), `
		SELECT count(*)
		FROM pg_inherits i
		JOIN pg_class p ON i.inhparent = p.oid
		JOIN pg_namespace pn ON p.relnamespace = pn.oid
		JOIN pg_class c ON i.inhrelid = c.oid
		JOIN pg_namespace cn ON c.relnamespace = cn.oid
		WHERE pn.nspname = $1 AND p.relname = $2
		  AND cn.nspname = $3 AND c.relname = $4
	`, parentSchema, parentTable, childSchema, childTable).Scan(&n)
	if err != nil {
		t.Fatalf("pg_inherits lookup: %v", err)
	}
	return n > 0
}

func partitionStatus(t *testing.T, pool *pgxpool.Pool, fq string) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(t.Context(),
		`SELECT status FROM partman.partitions WHERE name = $1`, fq,
	).Scan(&s); err != nil {
		t.Fatalf("status lookup for %s: %v", fq, err)
	}
	return s
}

// -----------------------------------------------------------------------------
// Sweep tests
// -----------------------------------------------------------------------------

// The clock in every Sweep test is 2026-06-15 12:00 UTC. Retention is
// 30 days, so cutoff is 2026-05-16 12:00 UTC. Bounds:
//   Expired : [2026-04-15, 2026-04-16)   bounds_to = 2026-04-16 (< cutoff)
//   Current : [2026-06-14, 2026-06-15)   bounds_to = 2026-06-15 (> cutoff)
//   Future  : [2026-07-01, 2026-07-02)   bounds_to = 2026-07-02 (> cutoff)

var (
	nowFixed  = time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	pastFrom  = time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)
	pastTo    = time.Date(2026, 4, 16, 0, 0, 0, 0, time.UTC)
	currFrom  = time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)
	currTo    = time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	futFrom   = time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	futTo     = time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	past2From = time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	past2To   = time.Date(2026, 3, 16, 0, 0, 0, 0, time.UTC)
	past3From = time.Date(2026, 2, 15, 0, 0, 0, 0, time.UTC)
	past3To   = time.Date(2026, 2, 16, 0, 0, 0, 0, time.UTC)
)

func TestSweep_SelectsOnlyExpiredNoDefaultNoCurrentNoFuture(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool)

	expired := insertBoundedPartition(t, pool, f, "events_20260415", pastFrom, pastTo)
	current := insertBoundedPartition(t, pool, f, "events_20260614", currFrom, currTo)
	future := insertBoundedPartition(t, pool, f, "events_20260701", futFrom, futTo)
	defaultID := insertDefaultPartition(t, pool, f)

	r := newRetention(t, pool, nowFixed, nil)
	report, err := r.Sweep(t.Context(), retention.ParentRef{SchemaName: f.Schema, TableName: f.Table})
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if report.Considered != 1 {
		t.Errorf("Considered = %d, want 1", report.Considered)
	}
	if len(report.Dropped) != 1 {
		t.Fatalf("Dropped = %d, want 1", len(report.Dropped))
	}
	if report.Dropped[0].Bounds.From.UTC() != pastFrom {
		t.Errorf("Dropped[0].Bounds.From = %s, want %s", report.Dropped[0].Bounds.From, pastFrom)
	}

	// Expired partition is gone.
	if tableExists(t, pool, f.Schema, "events_20260415") {
		t.Error("expected expired child dropped")
	}
	if partitionStatus(t, pool, f.Schema+".events_20260415") != "dropped" {
		t.Errorf("expected status=dropped for expired")
	}

	// Current, future, and default untouched.
	for _, tbl := range []string{"events_20260614", "events_20260701", "events_default"} {
		if !tableExists(t, pool, f.Schema, tbl) {
			t.Errorf("expected %s to still exist", tbl)
		}
	}
	for _, id := range []string{current, future, defaultID} {
		var status string
		_ = pool.QueryRow(t.Context(), `SELECT status FROM partman.partitions WHERE id = $1`, id).Scan(&status)
		if status != "active" {
			t.Errorf("partition %s status = %s, want active", id, status)
		}
	}
	_ = expired
}

func TestSweep_ClockAdvancedTenYears_StillNoDefaultOrFuture(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool)

	insertBoundedPartition(t, pool, f, "events_20260614", currFrom, currTo)
	insertBoundedPartition(t, pool, f, "events_20260701", futFrom, futTo)
	insertDefaultPartition(t, pool, f)

	tenYearsAhead := nowFixed.AddDate(10, 0, 0)
	r := newRetention(t, pool, tenYearsAhead, nil)
	report, err := r.Sweep(t.Context(), retention.ParentRef{SchemaName: f.Schema, TableName: f.Table})
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	// Both bounded partitions are now expired; default is NOT.
	if report.Considered != 2 {
		t.Errorf("Considered = %d, want 2", report.Considered)
	}
	if !tableExists(t, pool, f.Schema, "events_default") {
		t.Error("default partition must NEVER be dropped")
	}
	if partitionStatus(t, pool, f.Schema+".events_default") != "active" {
		t.Error("default partition metadata must stay active")
	}
}

func TestSweep_NilHookTreatsAsDrop(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool)
	insertBoundedPartition(t, pool, f, "events_20260415", pastFrom, pastTo)

	r := newRetention(t, pool, nowFixed, nil)
	report, err := r.Sweep(t.Context(), retention.ParentRef{SchemaName: f.Schema, TableName: f.Table})
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(report.Dropped) != 1 {
		t.Errorf("Dropped = %d, want 1", len(report.Dropped))
	}
}

func TestSweep_HookDetachDetachesButKeepsTable(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool)
	insertBoundedPartition(t, pool, f, "events_20260415", pastFrom, pastTo)

	hook := func(_ context.Context, _ hooks.PartitionRef) hooks.HookDecision {
		return hooks.HookDetach
	}
	r := newRetention(t, pool, nowFixed, hook)
	report, err := r.Sweep(t.Context(), retention.ParentRef{SchemaName: f.Schema, TableName: f.Table})
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(report.Detached) != 1 {
		t.Fatalf("Detached = %d, want 1", len(report.Detached))
	}
	if !tableExists(t, pool, f.Schema, "events_20260415") {
		t.Error("detached partition table should still exist")
	}
	if isChildOf(t, pool, f.Schema, f.Table, f.Schema, "events_20260415") {
		t.Error("detached partition should no longer be in pg_inherits")
	}
	if partitionStatus(t, pool, f.Schema+".events_20260415") != "detached" {
		t.Error("expected status=detached")
	}
}

func TestSweep_HookArchiveMovesToArchiveSchema(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	archiveSchema := "arc_" + strings.ToLower(ulid.Make().String())
	f := setupParent(t, pool, withRetentionSchema(archiveSchema))
	insertBoundedPartition(t, pool, f, "events_20260415", pastFrom, pastTo)

	hook := func(_ context.Context, _ hooks.PartitionRef) hooks.HookDecision {
		return hooks.HookArchive
	}
	r := newRetention(t, pool, nowFixed, hook)
	report, err := r.Sweep(t.Context(), retention.ParentRef{SchemaName: f.Schema, TableName: f.Table})
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(report.Archived) != 1 {
		t.Fatalf("Archived = %d, want 1", len(report.Archived))
	}
	if tableExists(t, pool, f.Schema, "events_20260415") {
		t.Error("archived child should no longer exist in original schema")
	}
	if !tableExists(t, pool, archiveSchema, "events_20260415") {
		t.Errorf("archived child should now live in %s schema", archiveSchema)
	}
	if partitionStatus(t, pool, f.Schema+".events_20260415") != "detached" {
		t.Error("expected status=detached after archive")
	}
}

func TestSweep_HookArchiveMissingSchemaSkipsButReturnsNil(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool) // no retention_schema
	insertBoundedPartition(t, pool, f, "events_20260415", pastFrom, pastTo)

	hook := func(_ context.Context, _ hooks.PartitionRef) hooks.HookDecision {
		return hooks.HookArchive
	}
	r := newRetention(t, pool, nowFixed, hook)
	report, err := r.Sweep(t.Context(), retention.ParentRef{SchemaName: f.Schema, TableName: f.Table})
	if err != nil {
		t.Fatalf("Sweep should return nil on per-partition anomaly: %v", err)
	}
	if len(report.Skipped) != 1 {
		t.Fatalf("Skipped = %d, want 1", len(report.Skipped))
	}
	if !tableExists(t, pool, f.Schema, "events_20260415") {
		t.Error("child should be untouched when archive is misconfigured")
	}
	if partitionStatus(t, pool, f.Schema+".events_20260415") != "active" {
		t.Error("status should remain active when archive is misconfigured")
	}
}

func TestSweep_HookSkipReOffersOnNextSweep(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool)
	insertBoundedPartition(t, pool, f, "events_20260415", pastFrom, pastTo)

	hook := func(_ context.Context, _ hooks.PartitionRef) hooks.HookDecision {
		return hooks.HookSkip
	}
	r := newRetention(t, pool, nowFixed, hook)
	first, err := r.Sweep(t.Context(), retention.ParentRef{SchemaName: f.Schema, TableName: f.Table})
	if err != nil {
		t.Fatalf("first Sweep: %v", err)
	}
	if len(first.Skipped) != 1 {
		t.Fatalf("first Skipped = %d, want 1", len(first.Skipped))
	}
	if partitionStatus(t, pool, f.Schema+".events_20260415") != "active" {
		t.Error("skipped partition should stay active")
	}

	second, err := r.Sweep(t.Context(), retention.ParentRef{SchemaName: f.Schema, TableName: f.Table})
	if err != nil {
		t.Fatalf("second Sweep: %v", err)
	}
	if len(second.Skipped) != 1 {
		t.Fatalf("second Skipped = %d, want 1 (re-offered)", len(second.Skipped))
	}
}

func TestSweep_DryRunDoesNoDDLOrMetadataWrite(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool)
	insertBoundedPartition(t, pool, f, "events_20260415", pastFrom, pastTo)

	hook := func(_ context.Context, _ hooks.PartitionRef) hooks.HookDecision {
		return hooks.HookDetach
	}
	r := newRetention(t, pool, nowFixed, hook)
	report, err := r.Sweep(t.Context(), retention.ParentRef{SchemaName: f.Schema, TableName: f.Table}, retention.WithDryRun(true))
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if !report.DryRun {
		t.Error("report.DryRun should be true")
	}
	if len(report.Detached) != 1 {
		t.Errorf("Detached = %d, want 1 (intended fate)", len(report.Detached))
	}
	if !tableExists(t, pool, f.Schema, "events_20260415") {
		t.Error("dry-run must not touch DDL")
	}
	if !isChildOf(t, pool, f.Schema, f.Table, f.Schema, "events_20260415") {
		t.Error("dry-run must not detach")
	}
	if partitionStatus(t, pool, f.Schema+".events_20260415") != "active" {
		t.Error("dry-run must not update status")
	}
}

func TestSweep_ContinuesAfterMidPartitionFailure(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool)

	// Three expired candidates.
	insertBoundedPartition(t, pool, f, "events_20260215", past3From, past3To)
	insertBoundedPartition(t, pool, f, "events_20260315", past2From, past2To)
	insertBoundedPartition(t, pool, f, "events_20260415", pastFrom, pastTo)

	// Pre-drop the middle child out of band so the sweep's DROP fails
	// for that one row. The other two must still succeed.
	if _, err := pool.Exec(t.Context(),
		fmt.Sprintf(`DROP TABLE %s.%s`, quoteIdent(f.Schema), quoteIdent("events_20260315")),
	); err != nil {
		t.Fatalf("pre-drop middle child: %v", err)
	}

	r := newRetention(t, pool, nowFixed, nil)
	report, err := r.Sweep(t.Context(), retention.ParentRef{SchemaName: f.Schema, TableName: f.Table})
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if report.Considered != 3 {
		t.Errorf("Considered = %d, want 3", report.Considered)
	}
	if len(report.Dropped) != 2 {
		t.Errorf("Dropped = %d, want 2", len(report.Dropped))
	}
	if len(report.Skipped) != 1 {
		t.Errorf("Skipped = %d, want 1", len(report.Skipped))
	}

	// The two survivors were fully dropped.
	for _, tbl := range []string{"events_20260215", "events_20260415"} {
		if tableExists(t, pool, f.Schema, tbl) {
			t.Errorf("%s should have been dropped", tbl)
		}
		if partitionStatus(t, pool, f.Schema+"."+tbl) != "dropped" {
			t.Errorf("%s metadata should be dropped", tbl)
		}
	}
	// The failed one stays active for retry.
	if partitionStatus(t, pool, f.Schema+".events_20260315") != "active" {
		t.Error("failed partition metadata should remain active")
	}
}

func TestSweep_MissingParentReturnsErrParentNotFound(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	r := newRetention(t, pool, nowFixed, nil)
	_, err := r.Sweep(t.Context(), retention.ParentRef{SchemaName: "nope", TableName: "nada"})
	if err == nil {
		t.Fatal("expected ErrParentNotFound, got nil")
	}
	if !isParentNotFound(err) {
		t.Errorf("expected ErrParentNotFound, got %v", err)
	}
}

// -----------------------------------------------------------------------------
// DropAll tests
// -----------------------------------------------------------------------------

func TestDropAll_RemovesEveryChildIncludingDefault(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool)

	insertBoundedPartition(t, pool, f, "events_20260415", pastFrom, pastTo)
	insertBoundedPartition(t, pool, f, "events_20260614", currFrom, currTo)
	insertBoundedPartition(t, pool, f, "events_20260701", futFrom, futTo)
	insertDefaultPartition(t, pool, f)

	r := newRetention(t, pool, nowFixed, nil)
	if err := r.DropAll(t.Context(), f.Schema, f.Table); err != nil {
		t.Fatalf("DropAll: %v", err)
	}

	for _, tbl := range []string{"events_20260415", "events_20260614", "events_20260701", "events_default"} {
		if tableExists(t, pool, f.Schema, tbl) {
			t.Errorf("%s should have been dropped", tbl)
		}
	}
	// Every metadata row is 'dropped'.
	var activeCount int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM partman.partitions WHERE parent_table_id = $1 AND status <> 'dropped'`, f.ParentID,
	).Scan(&activeCount); err != nil {
		t.Fatalf("count non-dropped: %v", err)
	}
	if activeCount != 0 {
		t.Errorf("%d partitions still not dropped in metadata", activeCount)
	}
}

func TestDropAll_IsIdempotentOnSecondCall(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool)
	insertBoundedPartition(t, pool, f, "events_20260415", pastFrom, pastTo)
	insertDefaultPartition(t, pool, f)

	r := newRetention(t, pool, nowFixed, nil)
	if err := r.DropAll(t.Context(), f.Schema, f.Table); err != nil {
		t.Fatalf("first DropAll: %v", err)
	}
	if err := r.DropAll(t.Context(), f.Schema, f.Table); err != nil {
		t.Fatalf("second DropAll: %v", err)
	}
}

func TestDropAll_ReturnsNilWhenParentMetadataAbsent(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	r := newRetention(t, pool, nowFixed, nil)
	if err := r.DropAll(t.Context(), "nope", "nada"); err != nil {
		t.Errorf("DropAll on absent parent should be nil, got %v", err)
	}
}

func TestSweep_OnlineMode_DropsViaConcurrentDetach(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool)
	insertBoundedPartition(t, pool, f, "events_20260415", pastFrom, pastTo)

	r := newRetentionMode(t, pool, nowFixed, nil, true)
	report, err := r.Sweep(t.Context(), retention.ParentRef{SchemaName: f.Schema, TableName: f.Table})
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(report.Dropped) != 1 {
		t.Fatalf("Dropped = %d, want 1", len(report.Dropped))
	}
	if tableExists(t, pool, f.Schema, "events_20260415") {
		t.Error("expected expired child dropped")
	}
	if partitionStatus(t, pool, f.Schema+".events_20260415") != "dropped" {
		t.Errorf("expected status=dropped")
	}
}

// TestSweep_OnlineMode_DropsWithDefaultPresent locks the Postgres rule that
// DETACH PARTITION CONCURRENTLY fails with SQLSTATE 55000 while a DEFAULT
// partition is attached. Every gopartman parent keeps a DEFAULT, so online
// Sweep must still drop expired dated children and leave the DEFAULT attached.
func TestSweep_OnlineMode_DropsWithDefaultPresent(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool)
	insertDefaultPartition(t, pool, f)
	insertBoundedPartition(t, pool, f, "events_20260415", pastFrom, pastTo)

	detachDDL := fmt.Sprintf(
		`ALTER TABLE %s.%s DETACH PARTITION %s.%s CONCURRENTLY`,
		quoteIdent(f.Schema), quoteIdent(f.Table),
		quoteIdent(f.Schema), quoteIdent("events_20260415"),
	)
	_, err := pool.Exec(t.Context(), detachDDL)
	if err == nil {
		t.Fatal("expected DETACH CONCURRENTLY to fail while DEFAULT is attached")
	}
	errText := err.Error()
	if !strings.Contains(errText, "cannot detach partitions concurrently when a default partition exists") &&
		!strings.Contains(errText, "SQLSTATE 55000") {
		t.Fatalf("unexpected DETACH CONCURRENTLY error: %v", err)
	}

	r := newRetentionMode(t, pool, nowFixed, nil, true)
	report, err := r.Sweep(t.Context(), retention.ParentRef{SchemaName: f.Schema, TableName: f.Table})
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(report.Dropped) != 1 {
		t.Fatalf("Dropped = %d, want 1 (expired child must drop despite DEFAULT); Skipped = %d",
			len(report.Dropped), len(report.Skipped))
	}
	if tableExists(t, pool, f.Schema, "events_20260415") {
		t.Error("expected expired child dropped")
	}
	if !tableExists(t, pool, f.Schema, "events_default") {
		t.Error("default partition must remain")
	}
	if !isChildOf(t, pool, f.Schema, f.Table, f.Schema, "events_default") {
		t.Error("default partition must be re-attached after online Sweep")
	}
	if partitionStatus(t, pool, f.Schema+".events_default") != "active" {
		t.Error("default partition metadata must stay active")
	}
}

// TestSweep_OnlineMode_DropsTenantChildrenWithDefaultPresent mirrors production
// Maintain logs: several expired tenant-dated children under one parent DEFAULT
// were skipped with SQLSTATE 55000 and partitions_dropped=0. Online Sweep must
// drop all of them in one pass and leave the DEFAULT attached.
func TestSweep_OnlineMode_DropsTenantChildrenWithDefaultPresent(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool, withTenantColumn("project_id"))
	insertDefaultPartition(t, pool, f)

	tenantID := "01TESTPROJECT00000000000001"
	expired := []struct {
		name string
		from time.Time
		to   time.Time
	}{
		{fmt.Sprintf("events_%s_20260415", tenantID), pastFrom, pastTo},
		{fmt.Sprintf("events_%s_20260315", tenantID), past2From, past2To},
		{fmt.Sprintf("events_%s_20260215", tenantID), past3From, past3To},
	}
	for _, e := range expired {
		insertTenantPartition(t, pool, f, e.name, tenantID, e.from, e.to)
	}
	currentName := fmt.Sprintf("events_%s_20260614", tenantID)
	insertTenantPartition(t, pool, f, currentName, tenantID, currFrom, currTo)

	r := newRetentionMode(t, pool, nowFixed, nil, true)
	report, err := r.Sweep(t.Context(), retention.ParentRef{SchemaName: f.Schema, TableName: f.Table})
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(report.Dropped) != 3 {
		t.Fatalf("Dropped = %d, want 3; Skipped = %d", len(report.Dropped), len(report.Skipped))
	}
	for _, e := range expired {
		if tableExists(t, pool, f.Schema, e.name) {
			t.Errorf("expected expired child %s dropped", e.name)
		}
	}
	if !tableExists(t, pool, f.Schema, currentName) {
		t.Error("current tenant partition must remain")
	}
	if !isChildOf(t, pool, f.Schema, f.Table, f.Schema, "events_default") {
		t.Error("default partition must be re-attached after online Sweep")
	}
}

func TestSweep_OnlineMode_HookDetachWithDefaultPresent(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool)
	insertDefaultPartition(t, pool, f)
	insertBoundedPartition(t, pool, f, "events_20260415", pastFrom, pastTo)

	hook := func(_ context.Context, _ hooks.PartitionRef) hooks.HookDecision {
		return hooks.HookDetach
	}
	r := newRetentionMode(t, pool, nowFixed, hook, true)
	report, err := r.Sweep(t.Context(), retention.ParentRef{SchemaName: f.Schema, TableName: f.Table})
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(report.Detached) != 1 {
		t.Fatalf("Detached = %d, want 1; Skipped = %d", len(report.Detached), len(report.Skipped))
	}
	if !tableExists(t, pool, f.Schema, "events_20260415") {
		t.Error("detached partition table should still exist")
	}
	if isChildOf(t, pool, f.Schema, f.Table, f.Schema, "events_20260415") {
		t.Error("detached partition should no longer be in pg_inherits")
	}
	if !isChildOf(t, pool, f.Schema, f.Table, f.Schema, "events_default") {
		t.Error("default partition must be re-attached after online Sweep")
	}
}

func TestSweep_OnlineMode_HookArchiveWithDefaultPresent(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	archiveSchema := "arc_" + strings.ToLower(ulid.Make().String())
	f := setupParent(t, pool, withRetentionSchema(archiveSchema))
	insertDefaultPartition(t, pool, f)
	insertBoundedPartition(t, pool, f, "events_20260415", pastFrom, pastTo)

	hook := func(_ context.Context, _ hooks.PartitionRef) hooks.HookDecision {
		return hooks.HookArchive
	}
	r := newRetentionMode(t, pool, nowFixed, hook, true)
	report, err := r.Sweep(t.Context(), retention.ParentRef{SchemaName: f.Schema, TableName: f.Table})
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(report.Archived) != 1 {
		t.Fatalf("Archived = %d, want 1; Skipped = %d", len(report.Archived), len(report.Skipped))
	}
	if tableExists(t, pool, f.Schema, "events_20260415") {
		t.Error("archived child should no longer exist in original schema")
	}
	if !tableExists(t, pool, archiveSchema, "events_20260415") {
		t.Errorf("archived child should now live in %s schema", archiveSchema)
	}
	if !isChildOf(t, pool, f.Schema, f.Table, f.Schema, "events_default") {
		t.Error("default partition must be re-attached after online Sweep")
	}
}

func TestSweep_OnlineMode_AllowsInsertsDuringDrop(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool)
	insertDefaultPartition(t, pool, f)
	insertBoundedPartition(t, pool, f, "events_20260415", pastFrom, pastTo)
	// Current partition so inserts have a home while DEFAULT is briefly detached.
	insertBoundedPartition(t, pool, f, "events_20260614", currFrom, currTo)

	insertErr := make(chan error, 1)
	go func() {
		var last error
		for i := 0; i < 200; i++ {
			_, last = pool.Exec(context.Background(),
				fmt.Sprintf(`INSERT INTO %s.%s (created_at) VALUES ($1)`, quoteIdent(f.Schema), quoteIdent(f.Table)),
				currFrom.Add(time.Hour),
			)
			if last != nil {
				insertErr <- last
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		insertErr <- nil
	}()

	r := newRetentionMode(t, pool, nowFixed, nil, true)
	if _, err := r.Sweep(t.Context(), retention.ParentRef{SchemaName: f.Schema, TableName: f.Table}); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if err := <-insertErr; err != nil {
		t.Fatalf("concurrent insert failed during online drop: %v", err)
	}
	if tableExists(t, pool, f.Schema, "events_20260415") {
		t.Error("expected expired child dropped")
	}
	if !isChildOf(t, pool, f.Schema, f.Table, f.Schema, "events_default") {
		t.Error("default partition must be re-attached after online Sweep")
	}
}

func TestSweep_OnlineMode_ResumeAfterDetach(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool)
	insertDefaultPartition(t, pool, f)
	insertBoundedPartition(t, pool, f, "events_20260415", pastFrom, pastTo)

	// Simulate a crash after DEFAULT was detached for concurrent work but
	// before expired children finished and DEFAULT was re-attached.
	detachDefault := fmt.Sprintf(
		`ALTER TABLE %s.%s DETACH PARTITION %s.%s`,
		quoteIdent(f.Schema), quoteIdent(f.Table),
		quoteIdent(f.Schema), quoteIdent("events_default"),
	)
	if _, err := pool.Exec(t.Context(), detachDefault); err != nil {
		t.Fatalf("pre-detach default: %v", err)
	}
	detachChild := fmt.Sprintf(
		`ALTER TABLE %s.%s DETACH PARTITION %s.%s CONCURRENTLY`,
		quoteIdent(f.Schema), quoteIdent(f.Table),
		quoteIdent(f.Schema), quoteIdent("events_20260415"),
	)
	if _, err := pool.Exec(t.Context(), detachChild); err != nil {
		t.Fatalf("pre-detach child: %v", err)
	}

	r := newRetentionMode(t, pool, nowFixed, nil, true)
	report, err := r.Sweep(t.Context(), retention.ParentRef{SchemaName: f.Schema, TableName: f.Table})
	if err != nil {
		t.Fatalf("Sweep after partial detach: %v", err)
	}
	if len(report.Dropped) != 1 {
		t.Fatalf("Dropped = %d, want 1; Skipped = %d", len(report.Dropped), len(report.Skipped))
	}
	if tableExists(t, pool, f.Schema, "events_20260415") {
		t.Error("expected detached child dropped on resume")
	}
	if !isChildOf(t, pool, f.Schema, f.Table, f.Schema, "events_default") {
		t.Error("default partition must be re-attached on resume")
	}
}

// TestSweep_OnlineMode_ReattachesOrphanDefaultWithNoExpiredWork covers a
// crashed prior Sweep that left DEFAULT detached when this tick has nothing
// to drop. Online Sweep must still restore DEFAULT and must not error.
func TestSweep_OnlineMode_ReattachesOrphanDefaultWithNoExpiredWork(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool)
	insertDefaultPartition(t, pool, f)
	insertBoundedPartition(t, pool, f, "events_20260614", currFrom, currTo)

	detachDefault := fmt.Sprintf(
		`ALTER TABLE %s.%s DETACH PARTITION %s.%s`,
		quoteIdent(f.Schema), quoteIdent(f.Table),
		quoteIdent(f.Schema), quoteIdent("events_default"),
	)
	if _, err := pool.Exec(t.Context(), detachDefault); err != nil {
		t.Fatalf("pre-detach default: %v", err)
	}

	r := newRetentionMode(t, pool, nowFixed, nil, true)
	report, err := r.Sweep(t.Context(), retention.ParentRef{SchemaName: f.Schema, TableName: f.Table})
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if report.Considered != 0 || len(report.Dropped) != 0 {
		t.Fatalf("expected no expired work, got Considered=%d Dropped=%d", report.Considered, len(report.Dropped))
	}
	if !isChildOf(t, pool, f.Schema, f.Table, f.Schema, "events_default") {
		t.Error("default partition must be re-attached even when nothing expired")
	}
}

// -----------------------------------------------------------------------------
// helpers
// -----------------------------------------------------------------------------

func isParentNotFound(err error) bool {
	return errors.Is(err, partman.ErrParentNotFound)
}
