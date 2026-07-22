// Example multitenant: register a parent with a tenant column, then
// register two tenants. Each tenant gets its own set of bounded
// partitions. Maintain slides the premake window forward for every
// registered tenant.
//
// Run:
//
//	DATABASE_URL=postgres://user:pass@localhost/db go run ./examples/multitenant
package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	partman "github.com/jirevwe/go_partman"
)

type partitionRow struct {
	tenant    string
	name      string
	isDefault bool
	status    string
}

const (
	demoSchema = "partman_mt_demo"
	demoTable  = "orders"
	tenantCol  = "tenant_id"
)

var demoTenants = []string{"ACME", "GLOBEX"}

func main() {
	ctx := context.Background()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is not set. " +
			"Example: DATABASE_URL=postgres://user:pass@localhost/pg_part go run ./examples/multitenant")
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	if err := applyMigrations(ctx, pool); err != nil {
		log.Fatalf("apply migrations: %v", err)
	}
	if err := recreateDemoParent(ctx, pool); err != nil {
		log.Fatalf("recreate demo parent: %v", err)
	}

	startTime := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	clock := partman.NewSimulatedClock(startTime)

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))

	mgr, err := partman.New(
		partman.WithDB(pool),
		partman.WithClock(clock),
		partman.WithLogger(logger),
	)
	if err != nil {
		log.Fatalf("partman.New: %v", err)
	}

	// Register the parent with a tenant column. No physical partitions
	// yet — provisioning happens per tenant.
	parentRef := partman.ParentRef{SchemaName: demoSchema, TableName: demoTable}
	if err := mgr.RegisterParent(ctx, partman.ParentConfig{
		SchemaName:        demoSchema,
		TableName:         demoTable,
		TenantColumn:      tenantCol,
		PartitionBy:       "created_at",
		PartitionInterval: partman.PartitionDayInterval,
		Premake:           2,
		RetentionPeriod:   7 * 24 * time.Hour,
	}); err != nil {
		log.Fatalf("RegisterParent: %v", err)
	}
	fmt.Println("== Registered parent (no tenants yet)")
	printPartitionsByTenant(ctx, pool, clock.Now())

	for _, id := range demoTenants {
		if err := mgr.RegisterTenant(ctx, partman.TenantConfig{
			ParentSchema: demoSchema, ParentName: demoTable, TenantId: id,
		}); err != nil {
			log.Fatalf("RegisterTenant %s: %v", id, err)
		}
	}

	tenants, err := mgr.ListTenants(ctx, parentRef)
	if err != nil {
		log.Fatalf("ListTenants: %v", err)
	}
	fmt.Printf("== Registered %d tenants:\n", len(tenants))
	for _, t := range tenants {
		fmt.Printf("    - %s\n", t.TenantId)
	}
	printPartitionsByTenant(ctx, pool, clock.Now())

	// Advance the clock and run Maintain twice. Each tenant's premake
	// window slides forward together.
	for i := 1; i <= 2; i++ {
		clock.AdvanceTime(24 * time.Hour)
		if err := mgr.Maintain(ctx); err != nil {
			log.Fatalf("Maintain: %v", err)
		}
		fmt.Printf("== After +%dd Maintain\n", i)
		printPartitionsByTenant(ctx, pool, clock.Now())
	}
}

func applyMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	for _, m := range partman.Migrations() {
		if _, err := pool.Exec(ctx, m.SQL); err != nil {
			return fmt.Errorf("migration %04d_%s: %w", m.Version, m.Name, err)
		}
	}
	return nil
}

func recreateDemoParent(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx,
		`DELETE FROM partman.parent_tables WHERE schema_name = $1 AND table_name = $2`,
		demoSchema, demoTable); err != nil {
		return fmt.Errorf("clean metadata: %w", err)
	}
	if _, err := pool.Exec(ctx, `DROP SCHEMA IF EXISTS `+demoSchema+` CASCADE`); err != nil {
		return fmt.Errorf("drop schema: %w", err)
	}
	if _, err := pool.Exec(ctx, `CREATE SCHEMA `+demoSchema); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}
	ddl := fmt.Sprintf(
		`CREATE TABLE %s.%s (id BIGSERIAL, %s VARCHAR NOT NULL, created_at TIMESTAMPTZ NOT NULL) `+
			`PARTITION BY RANGE (%s, created_at)`,
		demoSchema, demoTable, tenantCol, tenantCol,
	)
	if _, err := pool.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("create parent: %w", err)
	}
	return nil
}

func printPartitionsByTenant(ctx context.Context, pool *pgxpool.Pool, now time.Time) {
	rows, err := pool.Query(ctx, `
		SELECT COALESCE(p.tenant_id, '<none>') AS t, p.name, p.is_default, p.status
		FROM partman.partitions p
		JOIN partman.parent_tables pt ON pt.id = p.parent_table_id
		WHERE pt.schema_name = $1 AND pt.table_name = $2
		ORDER BY t, p.is_default DESC, p.partition_bounds_from`,
		demoSchema, demoTable)
	if err != nil {
		log.Printf("query partitions: %v", err)
		return
	}
	defer rows.Close()

	byTenant := map[string][]partitionRow{}
	for rows.Next() {
		var r partitionRow
		if err := rows.Scan(&r.tenant, &r.name, &r.isDefault, &r.status); err != nil {
			log.Printf("scan: %v", err)
			return
		}
		byTenant[r.tenant] = append(byTenant[r.tenant], r)
	}

	keys := make([]string, 0, len(byTenant))
	for k := range byTenant {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	fmt.Printf("  clock=%s  buckets=%d (tenants + default)\n", now.Format("2006-01-02"), len(byTenant))
	for _, tid := range keys {
		fmt.Printf("  tenant=%s\n", tid)
		for _, r := range byTenant[tid] {
			suffix := ""
			if r.isDefault {
				suffix = " (default)"
			}
			fmt.Printf("    - %s [%s]%s\n", r.name, r.status, suffix)
		}
	}
}
