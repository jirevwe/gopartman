package drain

import (
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/jirevwe/go_partman/internal/naming"
)

func TestOptions_Resolved_Defaults(t *testing.T) {
	got := Options{}.resolved()
	if got.BatchSize != defaultBatchSize {
		t.Errorf("BatchSize default: got %d, want %d", got.BatchSize, defaultBatchSize)
	}
	if got.MaxBatches != 0 {
		t.Errorf("MaxBatches default: got %d, want 0", got.MaxBatches)
	}
	if got.Tenant != nil {
		t.Errorf("Tenant default: got %v, want nil", got.Tenant)
	}
}

func TestOptions_Resolved_OverridesRespected(t *testing.T) {
	tenant := "acme"
	got := Options{BatchSize: 500, MaxBatches: 3, Tenant: &tenant}.resolved()
	if got.BatchSize != 500 {
		t.Errorf("BatchSize: got %d, want 500", got.BatchSize)
	}
	if got.MaxBatches != 3 {
		t.Errorf("MaxBatches: got %d, want 3", got.MaxBatches)
	}
	if got.Tenant == nil || *got.Tenant != "acme" {
		t.Errorf("Tenant: got %v, want &acme", got.Tenant)
	}
}

func TestOptions_Resolved_NegativeMaxBatchesClamped(t *testing.T) {
	got := Options{MaxBatches: -5}.resolved()
	if got.MaxBatches != 0 {
		t.Errorf("MaxBatches clamped: got %d, want 0", got.MaxBatches)
	}
}

func TestGroupByBounds_DailyGroupsRowsIntoOneDay(t *testing.T) {
	rows := []batchRow{
		newBatchRow(1, "2026-03-15T09:00:00Z", "", false),
		newBatchRow(2, "2026-03-15T15:37:00Z", "", false),
		newBatchRow(3, "2026-03-16T00:00:01Z", "", false),
	}
	got, err := groupByBounds(rows, naming.PartitionDayInterval)
	if err != nil {
		t.Fatalf("groupByBounds: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d groups, want 2", len(got))
	}
	day1 := groupKey{Bounds: naming.Bounds{
		From: mustTime("2026-03-15T00:00:00Z"),
		To:   mustTime("2026-03-16T00:00:00Z"),
	}}
	day2 := groupKey{Bounds: naming.Bounds{
		From: mustTime("2026-03-16T00:00:00Z"),
		To:   mustTime("2026-03-17T00:00:00Z"),
	}}
	if len(got[day1]) != 2 {
		t.Errorf("day1 group size: got %d, want 2", len(got[day1]))
	}
	if len(got[day2]) != 1 {
		t.Errorf("day2 group size: got %d, want 1", len(got[day2]))
	}
}

func TestGroupByBounds_TenantSplitsGroups(t *testing.T) {
	rows := []batchRow{
		newBatchRow(1, "2026-03-15T09:00:00Z", "ACME", true),
		newBatchRow(2, "2026-03-15T15:00:00Z", "BETA", true),
	}
	got, err := groupByBounds(rows, naming.PartitionDayInterval)
	if err != nil {
		t.Fatalf("groupByBounds: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d groups, want 2", len(got))
	}
	for k, ctids := range got {
		if k.Tenant != "ACME" && k.Tenant != "BETA" {
			t.Errorf("unexpected tenant in group key: %q", k.Tenant)
		}
		if len(ctids) != 1 {
			t.Errorf("group %q size: got %d, want 1", k.Tenant, len(ctids))
		}
	}
}

func TestGroupByBounds_UnsupportedIntervalReturnsError(t *testing.T) {
	rows := []batchRow{newBatchRow(1, "2026-03-15T09:00:00Z", "", false)}
	_, err := groupByBounds(rows, 17*time.Minute)
	if err == nil {
		t.Fatal("want error for unsupported interval, got nil")
	}
}

func TestBuildReadBatch_NoTenantNoAnomaly(t *testing.T) {
	q := buildReadBatch(readParams{
		Schema:       "app",
		DefaultTable: "events_default",
		ControlCol:   "created_at",
		BatchSize:    1000,
	})
	if !strings.Contains(q.SQL, `SELECT ctid, "created_at" FROM "app"."events_default"`) {
		t.Errorf("SELECT shape wrong: %s", q.SQL)
	}
	if !strings.Contains(q.SQL, `WHERE "created_at" IS NOT NULL`) {
		t.Errorf("NULL filter missing: %s", q.SQL)
	}
	if !strings.HasSuffix(q.SQL, `ORDER BY "created_at" LIMIT $1 FOR UPDATE SKIP LOCKED`) {
		t.Errorf("ORDER/LIMIT shape wrong: %s", q.SQL)
	}
	if len(q.Args) != 1 || q.Args[0] != 1000 {
		t.Errorf("Args: got %v, want [1000]", q.Args)
	}
}

func TestBuildReadBatch_TenantFilterAdded(t *testing.T) {
	tenant := "ACME"
	q := buildReadBatch(readParams{
		Schema:       "app",
		DefaultTable: "events_default",
		ControlCol:   "created_at",
		TenantCol:    "tenant_id",
		Tenant:       &tenant,
		BatchSize:    500,
	})
	if !strings.Contains(q.SQL, `SELECT ctid, "created_at", "tenant_id"`) {
		t.Errorf("SELECT missing tenant col: %s", q.SQL)
	}
	if !strings.Contains(q.SQL, `AND "tenant_id" = $1`) {
		t.Errorf("tenant filter missing: %s", q.SQL)
	}
	if q.Args[0] != "ACME" || q.Args[1] != 500 {
		t.Errorf("Args: got %v, want [ACME 500]", q.Args)
	}
}

func TestBuildReadBatch_AnomalyExclusionsAppended(t *testing.T) {
	from := mustTime("2026-03-15T00:00:00Z")
	to := mustTime("2026-03-16T00:00:00Z")
	q := buildReadBatch(readParams{
		Schema:       "app",
		DefaultTable: "events_default",
		ControlCol:   "created_at",
		TenantCol:    "tenant_id",
		AnomalyKeys: []groupKey{
			{Tenant: "ACME", TenantOK: true, Bounds: naming.Bounds{From: from, To: to}},
		},
		BatchSize: 100,
	})
	if !strings.Contains(q.SQL, `AND NOT ("created_at" >= $1 AND "created_at" < $2 AND "tenant_id" IS NOT DISTINCT FROM $3)`) {
		t.Errorf("anomaly exclusion missing: %s", q.SQL)
	}
	if q.Args[2] != "ACME" || q.Args[3] != 100 {
		t.Errorf("Args tail: got %v, want [... ACME 100]", q.Args)
	}
}

func TestBuildReadBatch_NoTenantColumnAnomalyExclusion(t *testing.T) {
	from := mustTime("2026-03-15T00:00:00Z")
	to := mustTime("2026-03-16T00:00:00Z")
	q := buildReadBatch(readParams{
		Schema:       "app",
		DefaultTable: "events_default",
		ControlCol:   "created_at",
		AnomalyKeys:  []groupKey{{Bounds: naming.Bounds{From: from, To: to}}},
		BatchSize:    100,
	})
	if !strings.Contains(q.SQL, `AND NOT ("created_at" >= $1 AND "created_at" < $2)`) {
		t.Errorf("non-tenant anomaly exclusion missing: %s", q.SQL)
	}
	if strings.Contains(q.SQL, "IS NOT DISTINCT FROM") {
		t.Errorf("should not add tenant clause when TenantCol is empty: %s", q.SQL)
	}
}

func TestBuildMoveCTE_Shape(t *testing.T) {
	got := buildMoveCTE("app", "events_default", "app.events_20260315", []string{"id", "created_at", "payload"})
	want := `WITH moved AS (DELETE FROM "app"."events_default" WHERE ctid = ANY($1::tid[]) RETURNING "id", "created_at", "payload") INSERT INTO "app"."events_20260315" ("id", "created_at", "payload") SELECT "id", "created_at", "payload" FROM moved`
	if got != want {
		t.Errorf("buildMoveCTE:\ngot:  %s\nwant: %s", got, want)
	}
}

func TestBuildNullSummary_NoTenantColumn(t *testing.T) {
	got, args := buildNullSummary("app", "events_default", "created_at", "", nil)
	want := `SELECT count(*) FROM "app"."events_default" WHERE "created_at" IS NULL`
	if got != want {
		t.Errorf("nullSummary no-tenant:\ngot:  %s\nwant: %s", got, want)
	}
	if len(args) != 0 {
		t.Errorf("args: got %v, want []", args)
	}
}

func TestBuildNullSummary_WithTenantFilter(t *testing.T) {
	tenant := "ACME"
	got, args := buildNullSummary("app", "events_default", "created_at", "tenant_id", &tenant)
	want := `SELECT "tenant_id", count(*) FROM "app"."events_default" WHERE "created_at" IS NULL AND "tenant_id" = $1 GROUP BY "tenant_id"`
	if got != want {
		t.Errorf("nullSummary tenant-filter:\ngot:  %s\nwant: %s", got, want)
	}
	if len(args) != 1 || args[0] != "ACME" {
		t.Errorf("args: got %v, want [ACME]", args)
	}
}

func TestBuildNullSummary_AllTenants(t *testing.T) {
	got, _ := buildNullSummary("app", "events_default", "created_at", "tenant_id", nil)
	want := `SELECT "tenant_id", count(*) FROM "app"."events_default" WHERE "created_at" IS NULL GROUP BY "tenant_id"`
	if got != want {
		t.Errorf("nullSummary all-tenants:\ngot:  %s\nwant: %s", got, want)
	}
}

func TestSQLTenantArg_UnsetReturnsNil(t *testing.T) {
	if got := sqlTenantArg(groupKey{}); got != nil {
		t.Errorf("got %v, want nil", got)
	}
	if got := sqlTenantArg(groupKey{TenantOK: true, Tenant: ""}); got != nil {
		t.Errorf("empty tenant should be nil, got %v", got)
	}
	if got := sqlTenantArg(groupKey{TenantOK: true, Tenant: "ACME"}); got != "ACME" {
		t.Errorf("got %v, want ACME", got)
	}
}

func newBatchRow(idx uint32, ctrl, tenant string, tenantOK bool) batchRow {
	return batchRow{
		CTID:     pgtype.TID{BlockNumber: idx, OffsetNumber: uint16(idx), Valid: true},
		Control:  mustTime(ctrl),
		Tenant:   tenant,
		TenantOK: tenantOK,
	}
}

func mustTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}
