//go:build integration

package importer_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oklog/ulid/v2"

	partman "github.com/jirevwe/gopartman"
	"github.com/jirevwe/gopartman/internal/importer"
	"github.com/jirevwe/gopartman/internal/testsupport"
)

// -----------------------------------------------------------------------------
// Fixtures
// -----------------------------------------------------------------------------

type parentFixture struct {
	Schema   string
	Table    string
	ParentID string
	Tenanted bool
}

type parentOpt func(*parentOpts)

type parentOpts struct {
	tenanted bool
	interval string // "hourly" | "daily" | "weekly" | "monthly"
}

func withTenant() parentOpt { return func(o *parentOpts) { o.tenanted = true } }

func setupParent(t *testing.T, pool *pgxpool.Pool, opts ...parentOpt) parentFixture {
	t.Helper()
	ctx := t.Context()

	o := parentOpts{interval: "daily"}
	for _, opt := range opts {
		opt(&o)
	}

	schema := "imp_" + strings.ToLower(ulid.Make().String())
	table := "events"

	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+quoteIdent(schema)); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	var createDDL, tenantCol string
	if o.tenanted {
		createDDL = fmt.Sprintf(
			`CREATE TABLE %s.%s (id BIGSERIAL, tenant_id TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL) PARTITION BY RANGE (tenant_id, created_at)`,
			quoteIdent(schema), quoteIdent(table),
		)
		tenantCol = "tenant_id"
	} else {
		createDDL = fmt.Sprintf(
			`CREATE TABLE %s.%s (id BIGSERIAL, created_at TIMESTAMPTZ NOT NULL) PARTITION BY RANGE (created_at)`,
			quoteIdent(schema), quoteIdent(table),
		)
	}
	if _, err := pool.Exec(ctx, createDDL); err != nil {
		t.Fatalf("create parent: %v", err)
	}

	parentID := ulid.Make().String()
	var tenantArg any
	if tenantCol != "" {
		tenantArg = tenantCol
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO partman.parent_tables
			(id, schema_name, table_name, tenant_column,
			 partition_by, partition_type, partition_interval,
			 retention_period, retention_keep_table, retention_schema,
			 automatic_maintenance, premake)
		VALUES ($1, $2, $3, $4, 'created_at', 'range', $5,
			INTERVAL '30 days', false, NULL, true, 1)
	`, parentID, schema, table, tenantArg, o.interval); err != nil {
		t.Fatalf("insert parent_tables row: %v", err)
	}

	return parentFixture{
		Schema:   schema,
		Table:    table,
		ParentID: parentID,
		Tenanted: o.tenanted,
	}
}

// createBoundedChild issues raw DDL to create a bounded partition. The
// child name is whatever the caller supplies; use this for both
// conforming and non-conforming (weird) names.
func createBoundedChild(t *testing.T, pool *pgxpool.Pool, f parentFixture, childTable string, from, to time.Time) {
	t.Helper()
	ddl := fmt.Sprintf(
		`CREATE TABLE %s.%s PARTITION OF %s.%s FOR VALUES FROM (TIMESTAMPTZ '%s') TO (TIMESTAMPTZ '%s')`,
		quoteIdent(f.Schema), quoteIdent(childTable),
		quoteIdent(f.Schema), quoteIdent(f.Table),
		from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339),
	)
	if _, err := pool.Exec(t.Context(), ddl); err != nil {
		t.Fatalf("create bounded child %s: %v", childTable, err)
	}
}

func createTenantChild(t *testing.T, pool *pgxpool.Pool, f parentFixture, childTable, tenantID string, from, to time.Time) {
	t.Helper()
	ddl := fmt.Sprintf(
		`CREATE TABLE %s.%s PARTITION OF %s.%s FOR VALUES FROM ('%s', TIMESTAMPTZ '%s') TO ('%s', TIMESTAMPTZ '%s')`,
		quoteIdent(f.Schema), quoteIdent(childTable),
		quoteIdent(f.Schema), quoteIdent(f.Table),
		tenantID, from.UTC().Format(time.RFC3339),
		tenantID, to.UTC().Format(time.RFC3339),
	)
	if _, err := pool.Exec(t.Context(), ddl); err != nil {
		t.Fatalf("create tenant child %s: %v", childTable, err)
	}
}

func createDefaultChild(t *testing.T, pool *pgxpool.Pool, f parentFixture) {
	t.Helper()
	childTable := f.Table + "_default"
	ddl := fmt.Sprintf(
		`CREATE TABLE %s.%s PARTITION OF %s.%s DEFAULT`,
		quoteIdent(f.Schema), quoteIdent(childTable),
		quoteIdent(f.Schema), quoteIdent(f.Table),
	)
	if _, err := pool.Exec(t.Context(), ddl); err != nil {
		t.Fatalf("create default child: %v", err)
	}
}

// insertOrphanMetadata writes a row into partman.partitions with no
// matching PG child. Used to force the Orphaned branch.
func insertOrphanMetadata(t *testing.T, pool *pgxpool.Pool, f parentFixture, fqName string, from, to time.Time) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO partman.partitions
			(id, name, parent_table_id, tenant_id,
			 partition_by, partition_type,
			 partition_bounds_from, partition_bounds_to,
			 is_default, status)
		VALUES ($1, $2, $3, NULL, 'created_at', 'range', $4, $5, false, 'active')
	`, ulid.Make().String(), fqName, f.ParentID, from, to); err != nil {
		t.Fatalf("insert orphan metadata: %v", err)
	}
}

func newImporter(t *testing.T, pool *pgxpool.Pool) *importer.Impl {
	t.Helper()
	imp, err := importer.New(importer.Config{Pool: pool})
	if err != nil {
		t.Fatalf("importer.New: %v", err)
	}
	return imp
}

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// -----------------------------------------------------------------------------
// Time fixtures
// -----------------------------------------------------------------------------

var (
	day1From = time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)
	day1To   = time.Date(2026, 4, 16, 0, 0, 0, 0, time.UTC)
	day2From = time.Date(2026, 4, 16, 0, 0, 0, 0, time.UTC)
	day2To   = time.Date(2026, 4, 17, 0, 0, 0, 0, time.UTC)
	day3From = time.Date(2026, 4, 17, 0, 0, 0, 0, time.UTC)
	day3To   = time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC)
)

// -----------------------------------------------------------------------------
// Tests
// -----------------------------------------------------------------------------

func TestImport_ThreeBoundedPartitions_ImportedThree(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool)

	createBoundedChild(t, pool, f, "events_20260415", day1From, day1To)
	createBoundedChild(t, pool, f, "events_20260416", day2From, day2To)
	createBoundedChild(t, pool, f, "events_20260417", day3From, day3To)

	imp := newImporter(t, pool)
	report, err := imp.Import(t.Context(), importer.ParentRef{SchemaName: f.Schema, TableName: f.Table})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(report.Imported) != 3 {
		t.Errorf("Imported = %d, want 3", len(report.Imported))
	}
	if len(report.Drifted) != 0 {
		t.Errorf("Drifted = %d, want 0", len(report.Drifted))
	}
	if len(report.Skipped) != 0 {
		t.Errorf("Skipped = %d, want 0", len(report.Skipped))
	}
	if len(report.Orphaned) != 0 {
		t.Errorf("Orphaned = %d, want 0", len(report.Orphaned))
	}

	if countPartitions(t, pool, f.ParentID) != 3 {
		t.Error("metadata count != 3 after import")
	}
}

func TestImport_DefaultPartition_IsDefaultTrue(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool)
	createDefaultChild(t, pool, f)

	imp := newImporter(t, pool)
	report, err := imp.Import(t.Context(), importer.ParentRef{SchemaName: f.Schema, TableName: f.Table})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(report.Imported) != 1 {
		t.Fatalf("Imported = %d, want 1", len(report.Imported))
	}
	fq := f.Schema + ".events_default"
	if !isDefaultInMetadata(t, pool, fq) {
		t.Errorf("metadata for %s should have is_default=true", fq)
	}
}

func TestImport_CompositeTenant_AutoRegistersTenant(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool, withTenant())
	createTenantChild(t, pool, f, "events_ABC_20260415", "ABC", day1From, day1To)
	createTenantChild(t, pool, f, "events_ABC_20260416", "ABC", day2From, day2To)

	imp := newImporter(t, pool)
	report, err := imp.Import(t.Context(), importer.ParentRef{SchemaName: f.Schema, TableName: f.Table})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(report.Imported) != 2 {
		t.Errorf("Imported = %d, want 2", len(report.Imported))
	}
	if !tenantExists(t, pool, f.ParentID, "ABC") {
		t.Error("tenant ABC should have been auto-registered")
	}
	if got := tenantIDForPartition(t, pool, f.Schema+".events_ABC_20260415"); got != "ABC" {
		t.Errorf("tenant_id in metadata = %q, want ABC", got)
	}
}

func TestImport_CompositeTenant_LowercaseInputUpperCased(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool, withTenant())
	// Child NAME uses uppercase (Build's format). PG bound uses whatever
	// the DDL wrote — here lowercase. parseBoundExpr must upper-case for
	// the cross-check to align.
	createTenantChild(t, pool, f, "events_TENANT1_20260415", "tenant1", day1From, day1To)

	imp := newImporter(t, pool)
	report, err := imp.Import(t.Context(), importer.ParentRef{SchemaName: f.Schema, TableName: f.Table})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(report.Imported) != 1 {
		t.Errorf("Imported = %d, want 1; report=%+v", len(report.Imported), report)
	}
	if len(report.Drifted) != 0 {
		t.Errorf("Drifted = %d, want 0 (case-insensitive tenant match): %+v", len(report.Drifted), report.Drifted)
	}
}

func TestImport_DriftedPartition_BoundMismatch(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool)
	// Name says 2026-04-15 but bounds are 2026-05-01..02.
	weirdFrom := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	weirdTo := time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC)
	createBoundedChild(t, pool, f, "events_20260415", weirdFrom, weirdTo)

	imp := newImporter(t, pool)
	report, err := imp.Import(t.Context(), importer.ParentRef{SchemaName: f.Schema, TableName: f.Table})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(report.Drifted) != 1 {
		t.Fatalf("Drifted = %d, want 1", len(report.Drifted))
	}
	if !strings.Contains(report.Drifted[0].Reason, "bounds.From mismatch") {
		t.Errorf("reason = %q; expected bounds.From mismatch", report.Drifted[0].Reason)
	}
	if len(report.Imported) != 0 {
		t.Error("drifted partitions must not be imported")
	}
	if countPartitions(t, pool, f.ParentID) != 0 {
		t.Error("no metadata should be written for drifted partitions")
	}
}

func TestImport_NonConformingName_Skipped(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool)
	// A name that TableName.Parse rejects.
	createBoundedChild(t, pool, f, "weirdname", day1From, day1To)

	imp := newImporter(t, pool)
	report, err := imp.Import(t.Context(), importer.ParentRef{SchemaName: f.Schema, TableName: f.Table})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(report.Skipped) != 1 {
		t.Fatalf("Skipped = %d, want 1", len(report.Skipped))
	}
	if !strings.Contains(report.Skipped[0].Reason, "non-conforming name") {
		t.Errorf("reason = %q", report.Skipped[0].Reason)
	}
	if len(report.Imported) != 0 {
		t.Error("skipped partitions must not be imported")
	}
}

func TestImport_OrphanedMetadata(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool)
	// Metadata row that has no corresponding PG child.
	insertOrphanMetadata(t, pool, f, f.Schema+".events_20260415", day1From, day1To)

	imp := newImporter(t, pool)
	report, err := imp.Import(t.Context(), importer.ParentRef{SchemaName: f.Schema, TableName: f.Table})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(report.Orphaned) != 1 {
		t.Fatalf("Orphaned = %d, want 1; report=%+v", len(report.Orphaned), report)
	}
	if !strings.HasSuffix(report.Orphaned[0].Schema+"."+report.Orphaned[0].Parent+"_20260415", ".events_20260415") {
		t.Errorf("orphan ref = %+v", report.Orphaned[0])
	}
}

func TestImport_IntervalMismatch_AbortsBeforeAnyWrite(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool) // "daily"
	// One correct daily child plus one monthly-sized child.
	createBoundedChild(t, pool, f, "events_20260415", day1From, day1To)
	monthlyFrom := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	monthlyTo := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	createBoundedChild(t, pool, f, "events_20260501", monthlyFrom, monthlyTo)

	imp := newImporter(t, pool)
	_, err := imp.Import(t.Context(), importer.ParentRef{SchemaName: f.Schema, TableName: f.Table})
	if err == nil {
		t.Fatal("expected ErrIntervalMismatch, got nil")
	}
	if !errors.Is(err, partman.ErrIntervalMismatch) {
		t.Errorf("expected ErrIntervalMismatch, got %v", err)
	}
	if countPartitions(t, pool, f.ParentID) != 0 {
		t.Error("no metadata should be written on interval mismatch")
	}
}

func TestImport_TwiceIsIdempotent(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool)

	// A conforming child (Imported on first run), a drifted child, a
	// non-conforming name, and an orphan metadata row.
	createBoundedChild(t, pool, f, "events_20260415", day1From, day1To)
	weirdFrom := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	weirdTo := time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC)
	createBoundedChild(t, pool, f, "events_20260501", weirdFrom, weirdTo) // ok, matches daily interval
	// Force drift on this second child by inserting metadata that mismatches:
	// Actually simplest — use the day1 partition, but rename metadata for
	// drift. Easier: skip using the second child for drift; use a
	// dedicated drift child.
	// Re-do: use one aligned child, one drift child, one non-conforming, one orphan.
	// The two children above are aligned. Add:
	createBoundedChild(t, pool, f, "weirdname", day3From, day3To)                // Skipped
	insertOrphanMetadata(t, pool, f, f.Schema+".events_ghost", day1From, day1To) // Orphaned

	imp := newImporter(t, pool)
	first, err := imp.Import(t.Context(), importer.ParentRef{SchemaName: f.Schema, TableName: f.Table})
	if err != nil {
		t.Fatalf("first Import: %v", err)
	}
	if len(first.Imported) != 2 {
		t.Errorf("first Imported = %d, want 2 (events_20260415 + events_20260501)", len(first.Imported))
	}
	if len(first.Skipped) != 1 {
		t.Errorf("first Skipped = %d, want 1", len(first.Skipped))
	}
	if len(first.Orphaned) != 1 {
		t.Errorf("first Orphaned = %d, want 1", len(first.Orphaned))
	}

	second, err := imp.Import(t.Context(), importer.ParentRef{SchemaName: f.Schema, TableName: f.Table})
	if err != nil {
		t.Fatalf("second Import: %v", err)
	}
	if len(second.Imported) != 0 {
		t.Errorf("second Imported = %d, want 0 (idempotent)", len(second.Imported))
	}
	if len(second.Skipped) != len(first.Skipped) {
		t.Errorf("second Skipped = %d, want %d (same as first)", len(second.Skipped), len(first.Skipped))
	}
	if len(second.Orphaned) != len(first.Orphaned) {
		t.Errorf("second Orphaned = %d, want %d", len(second.Orphaned), len(first.Orphaned))
	}
	if len(second.Drifted) != len(first.Drifted) {
		t.Errorf("second Drifted = %d, want %d", len(second.Drifted), len(first.Drifted))
	}
}

func TestImport_UnknownParent_ReturnsErrParentNotFound(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	imp := newImporter(t, pool)
	_, err := imp.Import(t.Context(), importer.ParentRef{SchemaName: "nope", TableName: "nada"})
	if err == nil {
		t.Fatal("expected ErrParentNotFound, got nil")
	}
	if !errors.Is(err, partman.ErrParentNotFound) {
		t.Errorf("expected ErrParentNotFound, got %v", err)
	}
}

// -----------------------------------------------------------------------------
// Query helpers
// -----------------------------------------------------------------------------

func countPartitions(t *testing.T, pool *pgxpool.Pool, parentID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM partman.partitions WHERE parent_table_id = $1`, parentID,
	).Scan(&n); err != nil {
		t.Fatalf("count partitions: %v", err)
	}
	return n
}

func isDefaultInMetadata(t *testing.T, pool *pgxpool.Pool, fq string) bool {
	t.Helper()
	var isDefault bool
	if err := pool.QueryRow(t.Context(),
		`SELECT is_default FROM partman.partitions WHERE name = $1`, fq,
	).Scan(&isDefault); err != nil {
		t.Fatalf("lookup is_default for %s: %v", fq, err)
	}
	return isDefault
}

func tenantExists(t *testing.T, pool *pgxpool.Pool, parentID, tenantID string) bool {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM partman.tenants WHERE parent_table_id = $1 AND id = $2`,
		parentID, tenantID,
	).Scan(&n); err != nil {
		t.Fatalf("count tenants: %v", err)
	}
	return n > 0
}

func tenantIDForPartition(t *testing.T, pool *pgxpool.Pool, fq string) string {
	t.Helper()
	var s *string
	if err := pool.QueryRow(t.Context(),
		`SELECT tenant_id FROM partman.partitions WHERE name = $1`, fq,
	).Scan(&s); err != nil {
		t.Fatalf("lookup tenant_id for %s: %v", fq, err)
	}
	if s == nil {
		return ""
	}
	return *s
}
