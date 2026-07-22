// Example basic: register one date-partitioned parent, run Maintain
// against a simulated clock, and watch old partitions drop as the clock
// advances past the retention window.
//
// Run:
//
//	DATABASE_URL=postgres://user:pass@localhost/db go run ./examples/basic
package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	partman "github.com/jirevwe/go_partman"
)

const (
	demoSchema = "partman_basic_demo"
	demoTable  = "events"
)

func main() {
	ctx := context.Background()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is not set. " +
			"Example: DATABASE_URL=postgres://user:pass@localhost/pg_part go run ./examples/basic")
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

	// Premake=2 gives 1 current + 2 future daily partitions plus the
	// default. Retention is 3 days behind the clock.
	if err := mgr.RegisterParent(ctx, partman.ParentConfig{
		SchemaName:        demoSchema,
		TableName:         demoTable,
		PartitionBy:       "created_at",
		PartitionInterval: partman.PartitionDayInterval,
		Premake:           2,
		RetentionPeriod:   3 * 24 * time.Hour,
	}); err != nil {
		log.Fatalf("RegisterParent: %v", err)
	}

	fmt.Println("== Registered parent")
	printPartitions(ctx, pool, clock.Now())

	// Advance one day at a time. Each Maintain call slides the premake
	// window forward.
	for i := 1; i <= 3; i++ {
		clock.AdvanceTime(24 * time.Hour)
		if err := mgr.Maintain(ctx); err != nil {
			log.Fatalf("Maintain: %v", err)
		}
		fmt.Printf("== After +%dd Maintain\n", i)
		printPartitions(ctx, pool, clock.Now())
	}

	// Jump the clock past the retention window. The oldest bounded
	// partitions are now drop candidates.
	clock.AdvanceTime(10 * 24 * time.Hour)
	if err := mgr.Maintain(ctx); err != nil {
		log.Fatalf("Maintain: %v", err)
	}
	fmt.Println("== After +10d jump")
	printPartitions(ctx, pool, clock.Now())
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
		`CREATE TABLE %s.%s (id BIGSERIAL, created_at TIMESTAMPTZ NOT NULL) PARTITION BY RANGE (created_at)`,
		demoSchema, demoTable,
	)
	if _, err := pool.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("create parent: %w", err)
	}
	return nil
}

func printPartitions(ctx context.Context, pool *pgxpool.Pool, now time.Time) {
	rows, err := pool.Query(ctx, `
		SELECT p.name, p.is_default, p.status
		FROM partman.partitions p
		JOIN partman.parent_tables pt ON pt.id = p.parent_table_id
		WHERE pt.schema_name = $1 AND pt.table_name = $2
		ORDER BY p.is_default DESC, p.partition_bounds_from`,
		demoSchema, demoTable)
	if err != nil {
		log.Printf("query partitions: %v", err)
		return
	}
	defer rows.Close()

	type row struct {
		name      string
		isDefault bool
		status    string
	}
	var found []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.name, &r.isDefault, &r.status); err != nil {
			log.Printf("scan: %v", err)
			return
		}
		found = append(found, r)
	}
	fmt.Printf("  clock=%s  partitions=%d\n", now.Format("2006-01-02"), len(found))
	for _, r := range found {
		suffix := ""
		if r.isDefault {
			suffix = " (default)"
		}
		fmt.Printf("    - %s [%s]%s\n", r.name, r.status, suffix)
	}
}
