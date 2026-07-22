//go:build integration

package registry_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oklog/ulid/v2"

	partman "github.com/jirevwe/go_partman"
	"github.com/jirevwe/go_partman/internal/provisioner"
	"github.com/jirevwe/go_partman/internal/registry"
	"github.com/jirevwe/go_partman/internal/testsupport"
)

type fixture struct {
	Schema string
	Table  string
}

// createParentTable creates a user schema and a real partitioned parent
// table. tenantColumn is empty for no-tenant parents.
func createParentTable(t *testing.T, pool *pgxpool.Pool, tenantColumn string) fixture {
	t.Helper()
	ctx := t.Context()

	schema := "reg_" + strings.ToLower(ulid.Make().String())
	table := "events"
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+quoteIdent(schema)); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	var ddl string
	if tenantColumn == "" {
		ddl = fmt.Sprintf(
			`CREATE TABLE %s.%s (id BIGSERIAL, created_at TIMESTAMPTZ NOT NULL) PARTITION BY RANGE (created_at)`,
			quoteIdent(schema), quoteIdent(table),
		)
	} else {
		ddl = fmt.Sprintf(
			`CREATE TABLE %s.%s (id BIGSERIAL, %s VARCHAR NOT NULL, created_at TIMESTAMPTZ NOT NULL) PARTITION BY RANGE (%s, created_at)`,
			quoteIdent(schema), quoteIdent(table), quoteIdent(tenantColumn), quoteIdent(tenantColumn),
		)
	}
	if _, err := pool.Exec(ctx, ddl); err != nil {
		t.Fatalf("create parent: %v", err)
	}
	return fixture{Schema: schema, Table: table}
}

// createUnpartitionedTable creates a plain (non-partitioned) table so we
// can prove ErrTargetNotPartitioned.
func createUnpartitionedTable(t *testing.T, pool *pgxpool.Pool) fixture {
	t.Helper()
	schema := "reg_" + strings.ToLower(ulid.Make().String())
	table := "plain"
	if _, err := pool.Exec(t.Context(), "CREATE SCHEMA "+quoteIdent(schema)); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := pool.Exec(t.Context(),
		fmt.Sprintf(`CREATE TABLE %s.%s (id BIGSERIAL, created_at TIMESTAMPTZ NOT NULL)`, quoteIdent(schema), quoteIdent(table)),
	); err != nil {
		t.Fatalf("create table: %v", err)
	}
	return fixture{Schema: schema, Table: table}
}

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
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

func countMetadataPartitions(t *testing.T, pool *pgxpool.Pool, parentSchema, parentTable string, tenantID string) int {
	t.Helper()
	var n int
	if tenantID == "" {
		err := pool.QueryRow(t.Context(), `
			SELECT count(*)
			FROM partman.partitions p
			JOIN partman.parent_tables pt ON pt.id = p.parent_table_id
			WHERE pt.schema_name = $1 AND pt.table_name = $2 AND p.tenant_id IS NULL
		`, parentSchema, parentTable).Scan(&n)
		if err != nil {
			t.Fatalf("count metadata: %v", err)
		}
		return n
	}
	err := pool.QueryRow(t.Context(), `
		SELECT count(*)
		FROM partman.partitions p
		JOIN partman.parent_tables pt ON pt.id = p.parent_table_id
		WHERE pt.schema_name = $1 AND pt.table_name = $2 AND p.tenant_id = $3
	`, parentSchema, parentTable, tenantID).Scan(&n)
	if err != nil {
		t.Fatalf("count metadata: %v", err)
	}
	return n
}

func parentRowExists(t *testing.T, pool *pgxpool.Pool, schema, table string) bool {
	t.Helper()
	var n int
	err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM partman.parent_tables WHERE schema_name = $1 AND table_name = $2`,
		schema, table).Scan(&n)
	if err != nil {
		t.Fatalf("count parent: %v", err)
	}
	return n == 1
}

func newRegistry(t *testing.T, pool *pgxpool.Pool, clockAt time.Time) *registry.Impl {
	t.Helper()
	prov, err := provisioner.New(provisioner.Config{
		Pool:  pool,
		Clock: partman.NewSimulatedClock(clockAt),
	})
	if err != nil {
		t.Fatalf("provisioner.New: %v", err)
	}
	reg, err := registry.New(registry.Config{
		Pool:        pool,
		Provisioner: prov,
	})
	if err != nil {
		t.Fatalf("registry.New: %v", err)
	}
	return reg
}

func baseParentConfig(f fixture, tenantCol string) registry.ParentConfig {
	return registry.ParentConfig{
		SchemaName:        f.Schema,
		TableName:         f.Table,
		TenantColumn:      tenantCol,
		PartitionBy:       "created_at",
		PartitionInterval: partman.PartitionDayInterval,
		Premake:           2,
		RetentionPeriod:   30 * 24 * time.Hour,
	}
}

func TestRegisterParent_NonPartitioned_ReturnsErrTargetNotPartitioned(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := createUnpartitionedTable(t, pool)
	reg := newRegistry(t, pool, time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC))

	err := reg.RegisterParent(t.Context(), baseParentConfig(f, ""))
	if !errors.Is(err, partman.ErrTargetNotPartitioned) {
		t.Fatalf("want ErrTargetNotPartitioned, got %v", err)
	}
	if parentRowExists(t, pool, f.Schema, f.Table) {
		t.Errorf("parent row was written even though validation failed")
	}
}

func TestRegisterParent_MissingPartitionByColumn_ReturnsErrColumnMissing(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := createParentTable(t, pool, "")
	reg := newRegistry(t, pool, time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC))

	cfg := baseParentConfig(f, "")
	cfg.PartitionBy = "does_not_exist"

	err := reg.RegisterParent(t.Context(), cfg)
	if !errors.Is(err, partman.ErrColumnMissing) {
		t.Fatalf("want ErrColumnMissing, got %v", err)
	}
}

func TestRegisterParent_MissingRetentionSchema_ReturnsErrArchiveSchemaMissing(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := createParentTable(t, pool, "")
	reg := newRegistry(t, pool, time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC))

	cfg := baseParentConfig(f, "")
	cfg.RetentionSchema = "definitely_not_a_schema"

	err := reg.RegisterParent(t.Context(), cfg)
	if !errors.Is(err, partman.ErrArchiveSchemaMissing) {
		t.Fatalf("want ErrArchiveSchemaMissing, got %v", err)
	}
}

func TestRegisterParent_NoTenant_ProvisionsInitialPartitions(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := createParentTable(t, pool, "")
	reg := newRegistry(t, pool, time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC))

	if err := reg.RegisterParent(t.Context(), baseParentConfig(f, "")); err != nil {
		t.Fatalf("RegisterParent: %v", err)
	}
	// premake=2 + current period = 3 bounded + 1 default = 4 children.
	if got := countChildPartitions(t, pool, f.Schema, f.Table); got != 4 {
		t.Errorf("child count = %d, want 4", got)
	}
	if got := countMetadataPartitions(t, pool, f.Schema, f.Table, ""); got != 4 {
		t.Errorf("metadata rows = %d, want 4", got)
	}
}

func TestRegisterParent_Tenanted_SkipsProvisionUntilTenant(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := createParentTable(t, pool, "tenant_id")
	reg := newRegistry(t, pool, time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC))

	if err := reg.RegisterParent(t.Context(), baseParentConfig(f, "tenant_id")); err != nil {
		t.Fatalf("RegisterParent: %v", err)
	}
	// No tenant registered yet → 0 PG partitions and 0 metadata rows.
	if got := countChildPartitions(t, pool, f.Schema, f.Table); got != 0 {
		t.Errorf("child count = %d, want 0 before RegisterTenant", got)
	}
}

func TestRegisterTenant_ProvisionsForNewTenantOnly(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := createParentTable(t, pool, "tenant_id")
	reg := newRegistry(t, pool, time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC))

	if err := reg.RegisterParent(t.Context(), baseParentConfig(f, "tenant_id")); err != nil {
		t.Fatalf("RegisterParent: %v", err)
	}
	if err := reg.RegisterTenant(t.Context(), registry.TenantConfig{
		ParentSchema: f.Schema, ParentName: f.Table, TenantId: "TENANT1",
	}); err != nil {
		t.Fatalf("RegisterTenant TENANT1: %v", err)
	}
	firstCount := countMetadataPartitions(t, pool, f.Schema, f.Table, "TENANT1")
	if firstCount != 3 { // 1 current + 2 premake
		t.Errorf("TENANT1 metadata rows = %d, want 3", firstCount)
	}

	if err := reg.RegisterTenant(t.Context(), registry.TenantConfig{
		ParentSchema: f.Schema, ParentName: f.Table, TenantId: "TENANT2",
	}); err != nil {
		t.Fatalf("RegisterTenant TENANT2: %v", err)
	}
	if got := countMetadataPartitions(t, pool, f.Schema, f.Table, "TENANT1"); got != firstCount {
		t.Errorf("TENANT1 count changed after adding TENANT2: %d -> %d", firstCount, got)
	}
	if got := countMetadataPartitions(t, pool, f.Schema, f.Table, "TENANT2"); got != 3 {
		t.Errorf("TENANT2 metadata rows = %d, want 3", got)
	}
}

func TestRegisterTenant_NonTenantedParent_ReturnsErrParentNotTenanted(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := createParentTable(t, pool, "")
	reg := newRegistry(t, pool, time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC))
	if err := reg.RegisterParent(t.Context(), baseParentConfig(f, "")); err != nil {
		t.Fatalf("RegisterParent: %v", err)
	}
	err := reg.RegisterTenant(t.Context(), registry.TenantConfig{
		ParentSchema: f.Schema, ParentName: f.Table, TenantId: "TENANT1",
	})
	if !errors.Is(err, partman.ErrParentNotTenanted) {
		t.Fatalf("want ErrParentNotTenanted, got %v", err)
	}
}

func TestRegisterTenant_UnknownParent_ReturnsErrParentNotFound(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	reg := newRegistry(t, pool, time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC))
	err := reg.RegisterTenant(t.Context(), registry.TenantConfig{
		ParentSchema: "no_such_schema", ParentName: "no_such_table", TenantId: "TENANT1",
	})
	if !errors.Is(err, partman.ErrParentNotFound) {
		t.Fatalf("want ErrParentNotFound, got %v", err)
	}
}

func TestRegisterParent_Duplicate_ReturnsErrParentAlreadyExists(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := createParentTable(t, pool, "")
	reg := newRegistry(t, pool, time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC))
	if err := reg.RegisterParent(t.Context(), baseParentConfig(f, "")); err != nil {
		t.Fatalf("first RegisterParent: %v", err)
	}
	err := reg.RegisterParent(t.Context(), baseParentConfig(f, ""))
	if !errors.Is(err, partman.ErrParentAlreadyExists) {
		t.Fatalf("want ErrParentAlreadyExists, got %v", err)
	}
}

func TestRegisterTenant_Duplicate_ReturnsErrTenantAlreadyExists(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := createParentTable(t, pool, "tenant_id")
	reg := newRegistry(t, pool, time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC))
	if err := reg.RegisterParent(t.Context(), baseParentConfig(f, "tenant_id")); err != nil {
		t.Fatalf("RegisterParent: %v", err)
	}
	if err := reg.RegisterTenant(t.Context(), registry.TenantConfig{
		ParentSchema: f.Schema, ParentName: f.Table, TenantId: "TENANT1",
	}); err != nil {
		t.Fatalf("first RegisterTenant: %v", err)
	}
	err := reg.RegisterTenant(t.Context(), registry.TenantConfig{
		ParentSchema: f.Schema, ParentName: f.Table, TenantId: "TENANT1",
	})
	if !errors.Is(err, partman.ErrTenantAlreadyExists) {
		t.Fatalf("want ErrTenantAlreadyExists, got %v", err)
	}
}

func TestRemoveTenant_KeepsPGPartitionTables(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := createParentTable(t, pool, "tenant_id")
	reg := newRegistry(t, pool, time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC))
	if err := reg.RegisterParent(t.Context(), baseParentConfig(f, "tenant_id")); err != nil {
		t.Fatalf("RegisterParent: %v", err)
	}
	if err := reg.RegisterTenant(t.Context(), registry.TenantConfig{
		ParentSchema: f.Schema, ParentName: f.Table, TenantId: "TENANT1",
	}); err != nil {
		t.Fatalf("RegisterTenant: %v", err)
	}
	priorChildren := countChildPartitions(t, pool, f.Schema, f.Table)

	if err := reg.RemoveTenant(t.Context(), registry.TenantRef{
		ParentSchema: f.Schema, ParentName: f.Table, TenantId: "TENANT1",
	}); err != nil {
		t.Fatalf("RemoveTenant: %v", err)
	}
	// PG partition tables must still be present.
	if got := countChildPartitions(t, pool, f.Schema, f.Table); got != priorChildren {
		t.Errorf("child partitions changed: %d -> %d, want unchanged", priorChildren, got)
	}
	// Metadata rows for the tenant must be gone.
	if got := countMetadataPartitions(t, pool, f.Schema, f.Table, "TENANT1"); got != 0 {
		t.Errorf("TENANT1 metadata rows = %d, want 0 after RemoveTenant", got)
	}
}

func TestRemoveParent_Default_KeepsPGTablesRemovesMetadata(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := createParentTable(t, pool, "")
	reg := newRegistry(t, pool, time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC))
	if err := reg.RegisterParent(t.Context(), baseParentConfig(f, "")); err != nil {
		t.Fatalf("RegisterParent: %v", err)
	}
	priorChildren := countChildPartitions(t, pool, f.Schema, f.Table)
	if err := reg.RemoveParent(t.Context(), registry.ParentRef{
		SchemaName: f.Schema, TableName: f.Table,
	}); err != nil {
		t.Fatalf("RemoveParent: %v", err)
	}
	if parentRowExists(t, pool, f.Schema, f.Table) {
		t.Errorf("parent row still present after RemoveParent")
	}
	if got := countChildPartitions(t, pool, f.Schema, f.Table); got != priorChildren {
		t.Errorf("child partitions changed: %d -> %d, want unchanged", priorChildren, got)
	}
	if got := countMetadataPartitions(t, pool, f.Schema, f.Table, ""); got != 0 {
		t.Errorf("metadata rows = %d, want 0 after cascade delete", got)
	}
}

func TestRemoveParent_CascadeWithoutRetention_ReturnsError(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := createParentTable(t, pool, "")
	reg := newRegistry(t, pool, time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC))
	if err := reg.RegisterParent(t.Context(), baseParentConfig(f, "")); err != nil {
		t.Fatalf("RegisterParent: %v", err)
	}
	err := reg.RemoveParent(t.Context(), registry.ParentRef{
		SchemaName: f.Schema, TableName: f.Table,
	}, registry.WithCascadeDrop())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "WithCascadeDrop requires Retention") {
		t.Errorf("unexpected error message: %v", err)
	}
	// Metadata row must still be present.
	if !parentRowExists(t, pool, f.Schema, f.Table) {
		t.Errorf("parent row was deleted despite cascade error")
	}
}

func TestRemoveTenant_ValidateTenantTriggerRejectsRawInsert(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := createParentTable(t, pool, "tenant_id")
	reg := newRegistry(t, pool, time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC))
	if err := reg.RegisterParent(t.Context(), baseParentConfig(f, "tenant_id")); err != nil {
		t.Fatalf("RegisterParent: %v", err)
	}

	// Get parent id.
	var parentID string
	if err := pool.QueryRow(t.Context(),
		`SELECT id FROM partman.parent_tables WHERE schema_name = $1 AND table_name = $2`,
		f.Schema, f.Table).Scan(&parentID); err != nil {
		t.Fatalf("select parent id: %v", err)
	}

	// Attempt a direct INSERT with an unknown tenant_id. The trigger
	// (ADR-0002) must reject it.
	_, err := pool.Exec(t.Context(), `
		INSERT INTO partman.partitions
			(id, name, parent_table_id, tenant_id,
			 partition_by, partition_type,
			 partition_bounds_from, partition_bounds_to, is_default)
		VALUES ($1, $2, $3, 'GHOSTTENANT',
		        'created_at', 'range',
		        '2026-03-15T00:00:00Z', '2026-03-16T00:00:00Z', false)
	`, ulid.Make().String(), f.Schema+".manual_row", parentID)
	if err == nil {
		t.Fatal("expected trigger to reject the insert")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestManagerEndToEnd(t *testing.T) {
	pool, _ := testsupport.NewPG(t)
	f := createParentTable(t, pool, "tenant_id")

	mgr, err := partman.New(
		partman.WithDB(pool),
		partman.WithClock(partman.NewSimulatedClock(time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC))),
	)
	if err != nil {
		t.Fatalf("partman.New: %v", err)
	}

	if err := mgr.RegisterParent(t.Context(), partman.ParentConfig{
		SchemaName:        f.Schema,
		TableName:         f.Table,
		TenantColumn:      "tenant_id",
		PartitionBy:       "created_at",
		PartitionInterval: partman.PartitionDayInterval,
		Premake:           1,
		RetentionPeriod:   7 * 24 * time.Hour,
	}); err != nil {
		t.Fatalf("Manager.RegisterParent: %v", err)
	}

	if err := mgr.RegisterTenant(t.Context(), partman.TenantConfig{
		ParentSchema: f.Schema, ParentName: f.Table, TenantId: "TENANT1",
	}); err != nil {
		t.Fatalf("Manager.RegisterTenant: %v", err)
	}

	parents, err := mgr.ListParents(t.Context())
	if err != nil {
		t.Fatalf("Manager.ListParents: %v", err)
	}
	if len(parents) != 1 || parents[0].TableName != f.Table {
		t.Errorf("ListParents = %+v, want 1 row named %s", parents, f.Table)
	}
	if !parents[0].AutomaticMaintenance {
		t.Errorf("AutomaticMaintenance = false, want true (default)")
	}

	tenants, err := mgr.ListTenants(t.Context(), partman.ParentRef{SchemaName: f.Schema, TableName: f.Table})
	if err != nil {
		t.Fatalf("Manager.ListTenants: %v", err)
	}
	if len(tenants) != 1 || tenants[0].TenantId != "TENANT1" {
		t.Errorf("ListTenants = %+v, want 1 row TENANT1", tenants)
	}

	if err := mgr.RemoveTenant(t.Context(), partman.TenantRef{
		ParentSchema: f.Schema, ParentName: f.Table, TenantId: "TENANT1",
	}); err != nil {
		t.Fatalf("Manager.RemoveTenant: %v", err)
	}
	if err := mgr.RemoveParent(t.Context(), partman.ParentRef{SchemaName: f.Schema, TableName: f.Table}); err != nil {
		t.Fatalf("Manager.RemoveParent: %v", err)
	}

	parents, err = mgr.ListParents(t.Context())
	if err != nil {
		t.Fatalf("Manager.ListParents final: %v", err)
	}
	if len(parents) != 0 {
		t.Errorf("ListParents after remove = %+v, want empty", parents)
	}
}
