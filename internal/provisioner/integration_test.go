//go:build integration

package provisioner_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oklog/ulid/v2"

	partman "github.com/jirevwe/go_partman"
	"github.com/jirevwe/go_partman/internal/provisioner"
	"github.com/jirevwe/go_partman/internal/testsupport"
)

type parentFixture struct {
	Schema       string
	Table        string
	ParentID     string
	TenantColumn string
	Interval     string
}

// setupParent creates a user schema, a partitioned parent table, and
// the corresponding partman.parent_tables row. Returns the fixture
// details so tests can call EnsurePartitions and assert against PG.
//
// intervalLabel is one of "hourly", "daily", "weekly", "monthly".
// tenantColumn is empty for no-tenant parents.
func setupParent(t *testing.T, pool *pgxpool.Pool, intervalLabel, tenantColumn string, premake int32) parentFixture {
	t.Helper()
	ctx := t.Context()

	schema := "prov_" + strings.ToLower(ulid.Make().String())
	table := "events"
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+quoteIdent(schema)); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	var partitionBy, createDDL string
	if tenantColumn == "" {
		partitionBy = "created_at"
		createDDL = fmt.Sprintf(
			`CREATE TABLE %s.%s (id BIGSERIAL, created_at TIMESTAMPTZ NOT NULL) PARTITION BY RANGE (created_at)`,
			quoteIdent(schema), quoteIdent(table),
		)
	} else {
		partitionBy = tenantColumn + ", created_at"
		createDDL = fmt.Sprintf(
			`CREATE TABLE %s.%s (id BIGSERIAL, %s VARCHAR NOT NULL, created_at TIMESTAMPTZ NOT NULL) PARTITION BY RANGE (%s, created_at)`,
			quoteIdent(schema), quoteIdent(table), quoteIdent(tenantColumn), quoteIdent(tenantColumn),
		)
	}
	if _, err := pool.Exec(ctx, createDDL); err != nil {
		t.Fatalf("create parent: %v", err)
	}

	parentID := ulid.Make().String()
	var tenantColArg any
	if tenantColumn != "" {
		tenantColArg = tenantColumn
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO partman.parent_tables
			(id, schema_name, table_name, tenant_column,
			 partition_by, partition_type, partition_interval,
			 retention_period, retention_keep_table, retention_schema,
			 automatic_maintenance, premake)
		VALUES ($1, $2, $3, $4, $5, 'range', $6, INTERVAL '30 days', false, NULL, true, $7)
	`, parentID, schema, table, tenantColArg, partitionBy, intervalLabel, premake)
	if err != nil {
		t.Fatalf("insert parent_tables row: %v", err)
	}

	return parentFixture{
		Schema:       schema,
		Table:        table,
		ParentID:     parentID,
		TenantColumn: tenantColumn,
		Interval:     intervalLabel,
	}
}

func insertTenant(t *testing.T, pool *pgxpool.Pool, parentID, tenantID string) {
	t.Helper()
	_, err := pool.Exec(t.Context(),
		`INSERT INTO partman.tenants (id, parent_table_id) VALUES ($1, $2)`,
		tenantID, parentID,
	)
	if err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
}

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func countChildPartitions(t *testing.T, pool *pgxpool.Pool, parentSchema, parentTable string) int {
	t.Helper()
	var n int
	err := pool.QueryRow(t.Context(), `
		SELECT count(*)
		FROM pg_inherits i
		JOIN pg_class parent ON i.inhparent = parent.oid
		JOIN pg_namespace pn ON parent.relnamespace = pn.oid
		WHERE pn.nspname = $1 AND parent.relname = $2
	`, parentSchema, parentTable).Scan(&n)
	if err != nil {
		t.Fatalf("count children: %v", err)
	}
	return n
}

func partitionBoundExpr(t *testing.T, pool *pgxpool.Pool, schema, table string) string {
	t.Helper()
	var expr string
	err := pool.QueryRow(t.Context(), `
		SELECT pg_get_expr(c.relpartbound, c.oid)
		FROM pg_class c
		JOIN pg_namespace n ON c.relnamespace = n.oid
		WHERE n.nspname = $1 AND c.relname = $2
	`, schema, table).Scan(&expr)
	if err != nil {
		t.Fatalf("get bound expr for %s.%s: %v", schema, table, err)
	}
	return expr
}

func countMetadataPartitions(t *testing.T, pool *pgxpool.Pool, parentID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM partman.partitions WHERE parent_table_id = $1`, parentID,
	).Scan(&n); err != nil {
		t.Fatalf("count metadata: %v", err)
	}
	return n
}

func newProvisioner(t *testing.T, pool *pgxpool.Pool, clockAt time.Time) *provisioner.Impl {
	t.Helper()
	p, err := provisioner.New(provisioner.Config{
		Pool:  pool,
		Clock: partman.NewSimulatedClock(clockAt),
	})
	if err != nil {
		t.Fatalf("provisioner.New: %v", err)
	}
	return p
}

func TestEnsurePartitions_DailyCreatesCurrentAndPremake_NoTenant(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool, "daily", "", 2)

	clockAt := time.Date(2026, 3, 15, 13, 37, 0, 0, time.UTC)
	p := newProvisioner(t, pool, clockAt)

	rep, err := p.EnsurePartitions(t.Context(), provisioner.ParentRef{
		SchemaName: f.Schema, TableName: f.Table,
	}, nil)
	if err != nil {
		t.Fatalf("EnsurePartitions: %v", err)
	}
	if rep.BoundedCreated != 3 || !rep.DefaultCreated {
		t.Errorf("report = %+v, want {BoundedCreated:3, DefaultCreated:true}", rep)
	}

	// Verify DB state: 3 bounded + 1 default = 4 children.
	if got := countChildPartitions(t, pool, f.Schema, f.Table); got != 4 {
		t.Errorf("child partitions in pg_inherits = %d, want 4", got)
	}
	if got := countMetadataPartitions(t, pool, f.ParentID); got != 4 {
		t.Errorf("metadata rows = %d, want 4", got)
	}
	// Verify day boundaries align to 00:00 UTC.
	dayExpr := partitionBoundExpr(t, pool, f.Schema, "events_20260315")
	if !strings.Contains(dayExpr, "'2026-03-15 00:00:00+00'") || !strings.Contains(dayExpr, "'2026-03-16 00:00:00+00'") {
		t.Errorf("expected midnight UTC bounds, got: %s", dayExpr)
	}
}

func TestEnsurePartitions_IsIdempotentOnSecondCall(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool, "daily", "", 2)

	p := newProvisioner(t, pool, time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC))

	if _, err := p.EnsurePartitions(t.Context(), provisioner.ParentRef{SchemaName: f.Schema, TableName: f.Table}, nil); err != nil {
		t.Fatalf("first EnsurePartitions: %v", err)
	}
	firstChildren := countChildPartitions(t, pool, f.Schema, f.Table)
	firstMetadata := countMetadataPartitions(t, pool, f.ParentID)

	rep, err := p.EnsurePartitions(t.Context(), provisioner.ParentRef{SchemaName: f.Schema, TableName: f.Table}, nil)
	if err != nil {
		t.Fatalf("second EnsurePartitions: %v", err)
	}
	if rep.BoundedCreated != 0 || rep.DefaultCreated {
		t.Errorf("second-call report = %+v, want zero-work report", rep)
	}
	if got := countChildPartitions(t, pool, f.Schema, f.Table); got != firstChildren {
		t.Errorf("children count changed on idempotent call: %d -> %d", firstChildren, got)
	}
	if got := countMetadataPartitions(t, pool, f.ParentID); got != firstMetadata {
		t.Errorf("metadata rows changed on idempotent call: %d -> %d", firstMetadata, got)
	}
}

func TestEnsurePartitions_MonthlyAlignsToCalendarBoundaries(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool, "monthly", "", 2)

	// ADR acceptance case.
	p := newProvisioner(t, pool, time.Date(2026, 1, 31, 23, 59, 59, 0, time.UTC))

	rep, err := p.EnsurePartitions(t.Context(), provisioner.ParentRef{SchemaName: f.Schema, TableName: f.Table}, nil)
	if err != nil {
		t.Fatalf("EnsurePartitions: %v", err)
	}
	if rep.BoundedCreated != 3 || !rep.DefaultCreated {
		t.Errorf("report = %+v, want {BoundedCreated:3, DefaultCreated:true}", rep)
	}

	// Assert the three monthly children exist with correct 1st-of-month bounds.
	janExpr := partitionBoundExpr(t, pool, f.Schema, "events_20260101")
	if !strings.Contains(janExpr, "'2026-01-01 00:00:00+00'") || !strings.Contains(janExpr, "'2026-02-01 00:00:00+00'") {
		t.Errorf("January bounds mismatched: %s", janExpr)
	}
	febExpr := partitionBoundExpr(t, pool, f.Schema, "events_20260201")
	if !strings.Contains(febExpr, "'2026-02-01 00:00:00+00'") || !strings.Contains(febExpr, "'2026-03-01 00:00:00+00'") {
		t.Errorf("February bounds mismatched: %s", febExpr)
	}
	marExpr := partitionBoundExpr(t, pool, f.Schema, "events_20260301")
	if !strings.Contains(marExpr, "'2026-03-01 00:00:00+00'") || !strings.Contains(marExpr, "'2026-04-01 00:00:00+00'") {
		t.Errorf("March bounds mismatched: %s", marExpr)
	}
}

func TestEnsurePartitions_TenantCompositeForValues(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool, "daily", "tenant_id", 1)
	insertTenant(t, pool, f.ParentID, "TENANT1")

	p := newProvisioner(t, pool, time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC))

	rep, err := p.EnsurePartitions(t.Context(),
		provisioner.ParentRef{SchemaName: f.Schema, TableName: f.Table},
		&provisioner.TenantRef{TenantId: "TENANT1"},
	)
	if err != nil {
		t.Fatalf("EnsurePartitions: %v", err)
	}
	if rep.BoundedCreated != 2 || !rep.DefaultCreated {
		t.Errorf("report = %+v, want {BoundedCreated:2, DefaultCreated:true}", rep)
	}

	// Tenant child should be named events_TENANT1_20260315.
	expr := partitionBoundExpr(t, pool, f.Schema, "events_TENANT1_20260315")
	if !strings.Contains(expr, "'TENANT1'") {
		t.Errorf("expected TENANT1 literal in FOR VALUES: %s", expr)
	}
	if !strings.Contains(expr, "'2026-03-15 00:00:00+00'") {
		t.Errorf("expected day bound in FOR VALUES: %s", expr)
	}
}

func TestEnsurePartitions_TenantMissingWhenColumnDeclared_Errors(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool, "daily", "tenant_id", 1)

	p := newProvisioner(t, pool, time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC))

	if _, err := p.EnsurePartitions(t.Context(),
		provisioner.ParentRef{SchemaName: f.Schema, TableName: f.Table}, nil,
	); err == nil {
		t.Fatal("want error when tenant column set but tenant nil, got nil")
	}
}

func TestEnsurePartitions_TenantSuppliedWhenColumnAbsent_Errors(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool, "daily", "", 1)

	p := newProvisioner(t, pool, time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC))

	if _, err := p.EnsurePartitions(t.Context(),
		provisioner.ParentRef{SchemaName: f.Schema, TableName: f.Table},
		&provisioner.TenantRef{TenantId: "SHOULDNOTBEHERE"},
	); err == nil {
		t.Fatal("want error when tenant supplied for non-tenant parent, got nil")
	}
}

func TestEnsurePartitions_MissingParent_Errors(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	p := newProvisioner(t, pool, time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC))
	if _, err := p.EnsurePartitions(t.Context(),
		provisioner.ParentRef{SchemaName: "nope", TableName: "nada"}, nil,
	); err == nil {
		t.Fatal("want error for unregistered parent, got nil")
	}
}

func TestEnsurePartitions_MidLoopFailure_RollsBackFullTransaction(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool, "daily", "", 1)
	ctx := t.Context()

	// Pre-create a partition that overlaps the SECOND day the
	// Provisioner will try to make. The partition uses a name outside
	// the Provisioner's grammar so CREATE TABLE IF NOT EXISTS with the
	// computed name still tries to create — and overlaps.
	overlapDDL := fmt.Sprintf(
		`CREATE TABLE %s."events_manual" PARTITION OF %s.%s FOR VALUES FROM (TIMESTAMPTZ '2026-03-16 00:00:00+00') TO (TIMESTAMPTZ '2026-03-17 00:00:00+00')`,
		quoteIdent(f.Schema), quoteIdent(f.Schema), quoteIdent(f.Table),
	)
	if _, err := pool.Exec(ctx, overlapDDL); err != nil {
		t.Fatalf("pre-create overlap partition: %v", err)
	}

	// Clock at Mar 15. premake=1 → wants to create Mar-15 and Mar-16;
	// Mar-16 overlaps → transaction rolls back.
	p := newProvisioner(t, pool, time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC))

	_, err := p.EnsurePartitions(ctx, provisioner.ParentRef{SchemaName: f.Schema, TableName: f.Table}, nil)
	if err == nil {
		t.Fatal("want error from overlap collision, got nil")
	}

	// After rollback:
	//   - metadata table should have zero rows for this parent
	//   - Mar-15 partition (from Provisioner) should NOT exist
	//   - overlap partition should still exist
	if got := countMetadataPartitions(t, pool, f.ParentID); got != 0 {
		t.Errorf("metadata rows after rollback = %d, want 0", got)
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf(`SELECT '%s.events_20260315'::regclass`, f.Schema)); err == nil {
		t.Error("events_20260315 exists in pg_class; rollback did not remove it")
	}
	// Sanity: overlap partition still exists.
	if _, err := pool.Exec(ctx, fmt.Sprintf(`SELECT '%s.events_manual'::regclass`, f.Schema)); err != nil {
		t.Errorf("overlap partition should still exist: %v", err)
	}
}

func TestEnsurePartitions_RetryAfterRollbackSucceeds(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool, "daily", "", 1)
	ctx := t.Context()

	overlapDDL := fmt.Sprintf(
		`CREATE TABLE %s."events_manual" PARTITION OF %s.%s FOR VALUES FROM (TIMESTAMPTZ '2026-03-16 00:00:00+00') TO (TIMESTAMPTZ '2026-03-17 00:00:00+00')`,
		quoteIdent(f.Schema), quoteIdent(f.Schema), quoteIdent(f.Table),
	)
	if _, err := pool.Exec(ctx, overlapDDL); err != nil {
		t.Fatalf("pre-create overlap partition: %v", err)
	}

	p := newProvisioner(t, pool, time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC))

	// First call rolls back.
	if _, err := p.EnsurePartitions(ctx, provisioner.ParentRef{SchemaName: f.Schema, TableName: f.Table}, nil); err == nil {
		t.Fatal("expected rollback error")
	}

	// Remove the conflict so retry succeeds.
	if _, err := pool.Exec(ctx, fmt.Sprintf(`ALTER TABLE %s.%s DETACH PARTITION %s."events_manual"`, quoteIdent(f.Schema), quoteIdent(f.Table), quoteIdent(f.Schema))); err != nil {
		t.Fatalf("detach overlap: %v", err)
	}

	rep, err := p.EnsurePartitions(ctx, provisioner.ParentRef{SchemaName: f.Schema, TableName: f.Table}, nil)
	if err != nil {
		t.Fatalf("second EnsurePartitions: %v", err)
	}
	if rep.BoundedCreated != 2 || !rep.DefaultCreated {
		t.Errorf("retry report = %+v, want {BoundedCreated:2, DefaultCreated:true}", rep)
	}

	// Both computed bounded partitions plus the default should now exist.
	// (3 children in pg_inherits; the manual one was detached.)
	if got := countChildPartitions(t, pool, f.Schema, f.Table); got != 3 {
		t.Errorf("children after retry = %d, want 3", got)
	}
}

// keep the unused import check happy if the file is trimmed later
var _ = context.Background
