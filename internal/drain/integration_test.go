//go:build integration

package drain_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oklog/ulid/v2"

	"github.com/jirevwe/go_partman/internal/drain"
	"github.com/jirevwe/go_partman/internal/maintainer"
	"github.com/jirevwe/go_partman/internal/naming"
	"github.com/jirevwe/go_partman/internal/testsupport"
)

// -----------------------------------------------------------------------------
// Fixtures
// -----------------------------------------------------------------------------

type parentFixture struct {
	Schema     string
	Table      string
	ParentID   string
	HasTenant  bool
	ControlCol string
	TenantCol  string
	Interval   time.Duration
}

type parentOpt func(*parentOpts)

type parentOpts struct {
	tenantColumn    string
	extraColumnDDL  string
	nullableControl bool
}

func withTenantColumn(c string) parentOpt     { return func(o *parentOpts) { o.tenantColumn = c } }
func withExtraColumnDDL(ddl string) parentOpt { return func(o *parentOpts) { o.extraColumnDDL = ddl } }
func withNullableControl() parentOpt          { return func(o *parentOpts) { o.nullableControl = true } }

// setupParent creates a daily range-partitioned parent with id + created_at
// (+ optional tenant + extra columns). Registers the parent in partman.
// Creates the default partition as `<table>_default`.
func setupParent(t *testing.T, pool *pgxpool.Pool, opts ...parentOpt) parentFixture {
	t.Helper()
	ctx := t.Context()

	o := parentOpts{}
	for _, opt := range opts {
		opt(&o)
	}

	schema := "drn_" + strings.ToLower(ulid.Make().String())
	table := "events"
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+quoteIdent(schema)); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	createdNull := "NOT NULL"
	if o.nullableControl {
		createdNull = ""
	}
	columns := fmt.Sprintf("id BIGSERIAL, created_at TIMESTAMPTZ %s", createdNull)
	partitionBy := "created_at"
	if o.tenantColumn != "" {
		columns = fmt.Sprintf("id BIGSERIAL, %s TEXT NOT NULL, created_at TIMESTAMPTZ %s", quoteIdent(o.tenantColumn), createdNull)
		partitionBy = fmt.Sprintf("%s, created_at", quoteIdent(o.tenantColumn))
	}
	if o.extraColumnDDL != "" {
		columns = columns + ", " + o.extraColumnDDL
	}

	createDDL := fmt.Sprintf(
		`CREATE TABLE %s.%s (%s) PARTITION BY RANGE (%s)`,
		quoteIdent(schema), quoteIdent(table), columns, partitionBy,
	)
	if _, err := pool.Exec(ctx, createDDL); err != nil {
		t.Fatalf("create parent: %v", err)
	}

	// Default partition. We detach it immediately so tests can create
	// bounded targets without Postgres scanning the default for
	// conflicting rows (Postgres 16 refuses the CREATE PARTITION OF when
	// the default holds rows in the new bounds). The drain queries the
	// default by name, so it works whether the default is attached or
	// detached.
	defaultDDL := fmt.Sprintf(
		`CREATE TABLE %s.%s PARTITION OF %s.%s DEFAULT`,
		quoteIdent(schema), quoteIdent(table+"_default"),
		quoteIdent(schema), quoteIdent(table),
	)
	if _, err := pool.Exec(ctx, defaultDDL); err != nil {
		t.Fatalf("create default: %v", err)
	}
	detachDDL := fmt.Sprintf(
		`ALTER TABLE %s.%s DETACH PARTITION %s.%s`,
		quoteIdent(schema), quoteIdent(table),
		quoteIdent(schema), quoteIdent(table+"_default"),
	)
	if _, err := pool.Exec(ctx, detachDDL); err != nil {
		t.Fatalf("detach default: %v", err)
	}

	parentID := ulid.Make().String()
	var tenantArg any
	if o.tenantColumn != "" {
		tenantArg = o.tenantColumn
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO partman.parent_tables
			(id, schema_name, table_name, tenant_column,
			 partition_by, partition_type, partition_interval,
			 retention_period, retention_keep_table, retention_schema,
			 automatic_maintenance, premake)
		VALUES ($1, $2, $3, $4, 'created_at', 'range', 'daily',
			INTERVAL '30 days', false, NULL, true, 1)
	`, parentID, schema, table, tenantArg)
	if err != nil {
		t.Fatalf("insert parent_tables row: %v", err)
	}
	// Default partition metadata row.
	defaultID := ulid.Make().String()
	epoch := time.Time{}
	if _, err := pool.Exec(ctx, `
		INSERT INTO partman.partitions
			(id, name, parent_table_id, tenant_id,
			 partition_by, partition_type,
			 partition_bounds_from, partition_bounds_to,
			 is_default, status)
		VALUES ($1, $2, $3, NULL, 'created_at', 'range', $4, $4, true, 'active')
	`, defaultID, schema+"."+table+"_default", parentID, epoch); err != nil {
		t.Fatalf("insert default partitions row: %v", err)
	}

	return parentFixture{
		Schema:     schema,
		Table:      table,
		ParentID:   parentID,
		HasTenant:  o.tenantColumn != "",
		ControlCol: "created_at",
		TenantCol:  o.tenantColumn,
		Interval:   naming.PartitionDayInterval,
	}
}

// registerTenant inserts a partman.tenants row so subsequent partitions
// with tenant_id pass the validate_tenant_id trigger.
func registerTenant(t *testing.T, pool *pgxpool.Pool, f parentFixture, tenant string) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO partman.tenants (id, parent_table_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`, tenant, f.ParentID); err != nil {
		t.Fatalf("register tenant %s: %v", tenant, err)
	}
}

// createTargetPartition creates a bounded child + partitions metadata row
// for the given day bounds and optional tenant.
func createTargetPartition(t *testing.T, pool *pgxpool.Pool, f parentFixture, tenant string, from, to time.Time) {
	t.Helper()
	ctx := t.Context()

	childName := f.Table + "_"
	if tenant != "" {
		childName = childName + strings.ToUpper(tenant) + "_"
	}
	childName = childName + from.UTC().Format("20060102")

	var forValues string
	if tenant == "" {
		forValues = fmt.Sprintf(
			`FROM (TIMESTAMPTZ '%s') TO (TIMESTAMPTZ '%s')`,
			from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339),
		)
	} else {
		forValues = fmt.Sprintf(
			`FROM ('%s', TIMESTAMPTZ '%s') TO ('%s', TIMESTAMPTZ '%s')`,
			tenant, from.UTC().Format(time.RFC3339),
			tenant, to.UTC().Format(time.RFC3339),
		)
	}

	ddl := fmt.Sprintf(
		`CREATE TABLE %s.%s PARTITION OF %s.%s FOR VALUES %s`,
		quoteIdent(f.Schema), quoteIdent(childName),
		quoteIdent(f.Schema), quoteIdent(f.Table),
		forValues,
	)
	if _, err := pool.Exec(ctx, ddl); err != nil {
		t.Fatalf("create target child %s: %v", childName, err)
	}

	var tenantArg any
	if tenant != "" {
		tenantArg = tenant
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO partman.partitions
			(id, name, parent_table_id, tenant_id,
			 partition_by, partition_type,
			 partition_bounds_from, partition_bounds_to,
			 is_default, status)
		VALUES ($1, $2, $3, $4, 'created_at', 'range', $5, $6, false, 'active')
	`, ulid.Make().String(), f.Schema+"."+childName, f.ParentID, tenantArg, from, to); err != nil {
		t.Fatalf("insert partitions row: %v", err)
	}
}

// seedDefault inserts n rows straight into the default child table.
// This bypasses partition routing so the test can seed after a bounded
// target has already been created (Postgres 16 refuses to attach a new
// bounded partition when conflicting rows live in _default).
func seedDefault(t *testing.T, pool *pgxpool.Pool, f parentFixture, tenant string, ctrl time.Time, n int) {
	t.Helper()
	ctx := t.Context()

	defaultTable := f.Table + "_default"
	if !f.HasTenant {
		for i := 0; i < n; i++ {
			if _, err := pool.Exec(ctx, fmt.Sprintf(
				`INSERT INTO %s.%s (created_at) VALUES ($1)`,
				quoteIdent(f.Schema), quoteIdent(defaultTable),
			), ctrl); err != nil {
				t.Fatalf("seed row: %v", err)
			}
		}
		return
	}
	for i := 0; i < n; i++ {
		if _, err := pool.Exec(ctx, fmt.Sprintf(
			`INSERT INTO %s.%s (%s, created_at) VALUES ($1, $2)`,
			quoteIdent(f.Schema), quoteIdent(defaultTable), quoteIdent(f.TenantCol),
		), tenant, ctrl); err != nil {
			t.Fatalf("seed tenant row: %v", err)
		}
	}
}

// seedNullControl inserts n rows with created_at = NULL straight into
// the default child table.
func seedNullControl(t *testing.T, pool *pgxpool.Pool, f parentFixture, tenant string, n int) {
	t.Helper()
	ctx := t.Context()
	defaultTable := f.Table + "_default"
	for i := 0; i < n; i++ {
		var err error
		if f.HasTenant {
			_, err = pool.Exec(ctx, fmt.Sprintf(
				`INSERT INTO %s.%s (%s, created_at) VALUES ($1, NULL)`,
				quoteIdent(f.Schema), quoteIdent(defaultTable), quoteIdent(f.TenantCol),
			), tenant)
		} else {
			_, err = pool.Exec(ctx, fmt.Sprintf(
				`INSERT INTO %s.%s (created_at) VALUES (NULL)`,
				quoteIdent(f.Schema), quoteIdent(defaultTable),
			))
		}
		if err != nil {
			t.Fatalf("seed null row: %v", err)
		}
	}
}

func countRows(t *testing.T, pool *pgxpool.Pool, schema, table string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(),
		fmt.Sprintf(`SELECT count(*) FROM %s.%s`, quoteIdent(schema), quoteIdent(table)),
	).Scan(&n); err != nil {
		t.Fatalf("count rows in %s.%s: %v", schema, table, err)
	}
	return n
}

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func newDrain(t *testing.T, pool *pgxpool.Pool) *drain.Impl {
	t.Helper()
	d, err := drain.New(drain.Config{
		Pool:  pool,
		Clock: testsupport.NewSimulatedClock(t),
	})
	if err != nil {
		t.Fatalf("drain.New: %v", err)
	}
	return d
}

// -----------------------------------------------------------------------------
// Happy path + acceptance criteria
// -----------------------------------------------------------------------------

func TestPartitionData_Drain10kAcrossFiveDays(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool)

	days := []time.Time{
		time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 11, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 12, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 13, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 14, 0, 0, 0, 0, time.UTC),
	}
	// Seed rows BEFORE creating target partitions so they land in _default.
	for _, d := range days {
		seedDefault(t, pool, f, "", d.Add(12*time.Hour), 2000)
	}
	for _, d := range days {
		createTargetPartition(t, pool, f, "", d, d.AddDate(0, 0, 1))
	}
	if got := countRows(t, pool, f.Schema, f.Table+"_default"); got != 10000 {
		t.Fatalf("pre-drain default count: got %d, want 10000", got)
	}

	d := newDrain(t, pool)
	report, err := d.PartitionData(t.Context(),
		drain.ParentRef{SchemaName: f.Schema, TableName: f.Table},
		drain.Options{BatchSize: 1000},
	)
	if err != nil {
		t.Fatalf("PartitionData: %v", err)
	}
	if report.RowsMoved != 10000 {
		t.Errorf("RowsMoved = %d, want 10000", report.RowsMoved)
	}
	if report.BatchesRun != 10 {
		t.Errorf("BatchesRun = %d, want 10", report.BatchesRun)
	}
	if len(report.Anomalies) != 0 {
		t.Errorf("Anomalies = %d, want 0", len(report.Anomalies))
	}
	if got := countRows(t, pool, f.Schema, f.Table+"_default"); got != 0 {
		t.Errorf("post-drain default count: got %d, want 0", got)
	}
	// Each day partition should hold 2000 rows.
	for _, d := range days {
		child := f.Table + "_" + d.UTC().Format("20060102")
		if got := countRows(t, pool, f.Schema, child); got != 2000 {
			t.Errorf("post-drain %s count: got %d, want 2000", child, got)
		}
	}
}

func TestPartitionData_EmptyDefaultReturnsZeroReport(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool)

	d := newDrain(t, pool)
	report, err := d.PartitionData(t.Context(),
		drain.ParentRef{SchemaName: f.Schema, TableName: f.Table},
		drain.Options{},
	)
	if err != nil {
		t.Fatalf("PartitionData: %v", err)
	}
	if report.RowsMoved != 0 || report.BatchesRun != 0 {
		t.Errorf("empty default: got %+v, want {0,0,nil}", report)
	}
	if len(report.Anomalies) != 0 {
		t.Errorf("Anomalies should be empty, got %d", len(report.Anomalies))
	}
}

func TestPartitionData_MissingPartitionRecordsAnomaly(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool)

	// Seed 3 rows for a day with NO target partition.
	day := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	seedDefault(t, pool, f, "", day.Add(6*time.Hour), 3)

	d := newDrain(t, pool)
	report, err := d.PartitionData(t.Context(),
		drain.ParentRef{SchemaName: f.Schema, TableName: f.Table},
		drain.Options{BatchSize: 10},
	)
	if err != nil {
		t.Fatalf("PartitionData: %v", err)
	}
	if report.RowsMoved != 0 {
		t.Errorf("RowsMoved = %d, want 0", report.RowsMoved)
	}
	if len(report.Anomalies) != 1 {
		t.Fatalf("Anomalies len = %d, want 1", len(report.Anomalies))
	}
	a := report.Anomalies[0]
	if a.RowCount != 3 {
		t.Errorf("Anomaly RowCount = %d, want 3", a.RowCount)
	}
	if !a.MissingPartitionBounds.From.Equal(day) {
		t.Errorf("Anomaly Bounds.From = %s, want %s", a.MissingPartitionBounds.From, day)
	}
	if got := countRows(t, pool, f.Schema, f.Table+"_default"); got != 3 {
		t.Errorf("rows should stay in default: got %d, want 3", got)
	}
}

func TestPartitionData_AnomalyDoesNotBlockLaterHealthyRows(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool)

	// Anomalous rows sort first (earliest day) so without the exclusion
	// clause the drain would loop on them forever.
	anomalyDay := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	seedDefault(t, pool, f, "", anomalyDay.Add(6*time.Hour), 2)

	healthyDay := time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC)
	seedDefault(t, pool, f, "", healthyDay.Add(2*time.Hour), 5)
	createTargetPartition(t, pool, f, "", healthyDay, healthyDay.AddDate(0, 0, 1))

	d := newDrain(t, pool)
	report, err := d.PartitionData(t.Context(),
		drain.ParentRef{SchemaName: f.Schema, TableName: f.Table},
		drain.Options{BatchSize: 3},
	)
	if err != nil {
		t.Fatalf("PartitionData: %v", err)
	}
	if report.RowsMoved != 5 {
		t.Errorf("RowsMoved = %d, want 5", report.RowsMoved)
	}
	if len(report.Anomalies) != 1 || report.Anomalies[0].RowCount != 2 {
		t.Errorf("Anomalies = %+v, want [{RowCount:2}]", report.Anomalies)
	}
	if got := countRows(t, pool, f.Schema, f.Table+"_default"); got != 2 {
		t.Errorf("anomalous rows should remain in default: got %d, want 2", got)
	}
}

func TestPartitionData_TenantScopingMovesOneTenantOnly(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool, withTenantColumn("tenant_id"))

	day := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	seedDefault(t, pool, f, "ACME", day.Add(3*time.Hour), 4)
	seedDefault(t, pool, f, "BETA", day.Add(4*time.Hour), 3)
	registerTenant(t, pool, f, "ACME")
	registerTenant(t, pool, f, "BETA")
	createTargetPartition(t, pool, f, "ACME", day, day.AddDate(0, 0, 1))
	createTargetPartition(t, pool, f, "BETA", day, day.AddDate(0, 0, 1))

	tenant := "ACME"
	d := newDrain(t, pool)
	report, err := d.PartitionData(t.Context(),
		drain.ParentRef{SchemaName: f.Schema, TableName: f.Table},
		drain.Options{Tenant: &tenant},
	)
	if err != nil {
		t.Fatalf("PartitionData: %v", err)
	}
	if report.RowsMoved != 4 {
		t.Errorf("RowsMoved = %d, want 4", report.RowsMoved)
	}
	if got := countRows(t, pool, f.Schema, f.Table+"_ACME_"+day.Format("20060102")); got != 4 {
		t.Errorf("ACME child count: got %d, want 4", got)
	}
	if got := countRows(t, pool, f.Schema, f.Table+"_default"); got != 3 {
		t.Errorf("default should still have BETA rows: got %d, want 3", got)
	}
}

func TestPartitionData_NullControlColumnBecomesAnomaly(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool, withNullableControl())

	seedNullControl(t, pool, f, "", 4)

	d := newDrain(t, pool)
	report, err := d.PartitionData(t.Context(),
		drain.ParentRef{SchemaName: f.Schema, TableName: f.Table},
		drain.Options{},
	)
	if err != nil {
		t.Fatalf("PartitionData: %v", err)
	}
	if report.RowsMoved != 0 {
		t.Errorf("RowsMoved = %d, want 0", report.RowsMoved)
	}
	if len(report.Anomalies) != 1 {
		t.Fatalf("Anomalies len = %d, want 1", len(report.Anomalies))
	}
	a := report.Anomalies[0]
	if a.RowCount != 4 {
		t.Errorf("Anomaly RowCount = %d, want 4", a.RowCount)
	}
	if !a.MissingPartitionBounds.From.IsZero() || !a.MissingPartitionBounds.To.IsZero() {
		t.Errorf("null anomaly should have zero-value Bounds: got %+v", a.MissingPartitionBounds)
	}
	if got := countRows(t, pool, f.Schema, f.Table+"_default"); got != 4 {
		t.Errorf("null rows stay in default: got %d, want 4", got)
	}
}

func TestPartitionData_MissingParentReturnsError(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	d := newDrain(t, pool)
	_, err := d.PartitionData(t.Context(),
		drain.ParentRef{SchemaName: "nope", TableName: "nada"},
		drain.Options{},
	)
	if err == nil {
		t.Fatal("expected ErrParentNotFound, got nil")
	}
	if !errors.Is(err, drain.ErrParentNotFound) {
		t.Errorf("expected ErrParentNotFound, got %v", err)
	}
}

func TestPartitionData_LockContentionReturnsErrParentBusy(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool)

	// Grab the advisory lock in a separate session so the drain sees busy.
	conn, err := pool.Acquire(t.Context())
	if err != nil {
		t.Fatalf("acquire conn: %v", err)
	}
	defer conn.Release()
	locked, err := maintainer.TryLock(t.Context(), conn, f.Schema, f.Table)
	if err != nil {
		t.Fatalf("prime lock: %v", err)
	}
	if !locked {
		t.Fatal("prime lock: expected true, got false")
	}
	defer func() {
		_ = maintainer.Unlock(t.Context(), conn, f.Schema, f.Table)
	}()

	d := newDrain(t, pool)
	_, err = d.PartitionData(t.Context(),
		drain.ParentRef{SchemaName: f.Schema, TableName: f.Table},
		drain.Options{},
	)
	if !errors.Is(err, drain.ErrParentBusy) {
		t.Errorf("expected ErrParentBusy, got %v", err)
	}
}

func TestPartitionData_CancelMidDrainKeepsConsistentState(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool)

	day := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	seedDefault(t, pool, f, "", day.Add(3*time.Hour), 5000)
	createTargetPartition(t, pool, f, "", day, day.AddDate(0, 0, 1))

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	d := newDrain(t, pool)
	report, err := d.PartitionData(ctx,
		drain.ParentRef{SchemaName: f.Schema, TableName: f.Table},
		drain.Options{BatchSize: 200},
	)
	if err == nil {
		t.Log("drain completed before cancel; still valid — checking consistency")
	} else if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled or nil, got %v", err)
	}

	defaultCount := countRows(t, pool, f.Schema, f.Table+"_default")
	child := f.Table + "_" + day.UTC().Format("20060102")
	childCount := countRows(t, pool, f.Schema, child)
	total := defaultCount + childCount
	if total != 5000 {
		t.Errorf("row count after cancel: default=%d target=%d total=%d, want 5000", defaultCount, childCount, total)
	}
	t.Logf("moved before cancel: %d (BatchesRun=%d)", report.RowsMoved, report.BatchesRun)
}

func TestPartitionData_MaxBatchesLimitsWork(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool)

	day := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	seedDefault(t, pool, f, "", day.Add(3*time.Hour), 500)
	createTargetPartition(t, pool, f, "", day, day.AddDate(0, 0, 1))

	d := newDrain(t, pool)
	report, err := d.PartitionData(t.Context(),
		drain.ParentRef{SchemaName: f.Schema, TableName: f.Table},
		drain.Options{BatchSize: 100, MaxBatches: 2},
	)
	if err != nil {
		t.Fatalf("PartitionData: %v", err)
	}
	if report.BatchesRun != 2 {
		t.Errorf("BatchesRun = %d, want 2", report.BatchesRun)
	}
	if report.RowsMoved != 200 {
		t.Errorf("RowsMoved = %d, want 200", report.RowsMoved)
	}
	if got := countRows(t, pool, f.Schema, f.Table+"_default"); got != 300 {
		t.Errorf("default residual: got %d, want 300", got)
	}
}

func TestPartitionData_GeneratedColumnParent(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool,
		withExtraColumnDDL(`double_id BIGINT GENERATED ALWAYS AS (id * 2) STORED`),
	)

	day := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	seedDefault(t, pool, f, "", day.Add(1*time.Hour), 3)
	createTargetPartition(t, pool, f, "", day, day.AddDate(0, 0, 1))

	d := newDrain(t, pool)
	report, err := d.PartitionData(t.Context(),
		drain.ParentRef{SchemaName: f.Schema, TableName: f.Table},
		drain.Options{},
	)
	if err != nil {
		t.Fatalf("PartitionData: %v", err)
	}
	if report.RowsMoved != 3 {
		t.Errorf("RowsMoved = %d, want 3", report.RowsMoved)
	}
}

func TestPartitionData_TriggerFiresOnTargetInsert(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := setupParent(t, pool)

	day := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	seedDefault(t, pool, f, "", day.Add(2*time.Hour), 4)
	createTargetPartition(t, pool, f, "", day, day.AddDate(0, 0, 1))
	child := f.Table + "_" + day.UTC().Format("20060102")

	// Add a trigger that increments a counter row on every insert.
	ctx := t.Context()
	if _, err := pool.Exec(ctx, fmt.Sprintf(
		`CREATE TABLE %s.%s (n BIGINT)`, quoteIdent(f.Schema), quoteIdent("trig_count"),
	)); err != nil {
		t.Fatalf("create counter: %v", err)
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf(
		`INSERT INTO %s.%s VALUES (0)`, quoteIdent(f.Schema), quoteIdent("trig_count"),
	)); err != nil {
		t.Fatalf("seed counter: %v", err)
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE FUNCTION %s.bump_counter() RETURNS trigger AS $$
		BEGIN
			UPDATE %s.%s SET n = n + 1;
			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql`,
		quoteIdent(f.Schema), quoteIdent(f.Schema), quoteIdent("trig_count"),
	)); err != nil {
		t.Fatalf("create fn: %v", err)
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE TRIGGER bump AFTER INSERT ON %s.%s
		FOR EACH ROW EXECUTE FUNCTION %s.bump_counter()`,
		quoteIdent(f.Schema), quoteIdent(child), quoteIdent(f.Schema),
	)); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	d := newDrain(t, pool)
	if _, err := d.PartitionData(t.Context(),
		drain.ParentRef{SchemaName: f.Schema, TableName: f.Table},
		drain.Options{},
	); err != nil {
		t.Fatalf("PartitionData: %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx, fmt.Sprintf(
		`SELECT n FROM %s.%s`, quoteIdent(f.Schema), quoteIdent("trig_count"),
	)).Scan(&n); err != nil {
		t.Fatalf("read counter: %v", err)
	}
	if n != 4 {
		t.Errorf("trigger fired %d times, want 4", n)
	}
}
