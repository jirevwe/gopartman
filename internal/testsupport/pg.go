// Package testsupport gives integration-test helpers for the partman
// library. The harness starts one PostgreSQL container per test binary
// and shares it across all tests in that binary.
package testsupport

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpg "github.com/testcontainers/testcontainers-go/modules/postgres"

	partman "github.com/jirevwe/gopartman"
)

var (
	containerOnce sync.Once
	containerDSN  string
	containerErr  error
)

const containerImage = "postgres:16-alpine"

// NewPG returns a pgxpool.Pool bound to a shared PostgreSQL container.
// The container starts on the first call in a test binary. Ryuk
// removes the container when the process exits.
//
// The returned cleanup truncates the partman schema and drops any
// non-system schemas that appeared during the test. Cleanup also runs
// automatically via t.Cleanup, so tests do not have to invoke it.
func NewPG(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()

	containerOnce.Do(startContainer)
	if containerErr != nil {
		t.Fatalf("start postgres container: %v", containerErr)
	}

	ctx := t.Context()
	pool, err := pgxpool.New(ctx, containerDSN)
	if err != nil {
		t.Fatalf("connect to postgres: %v", err)
	}

	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() { resetPool(t, pool) })
	}
	t.Cleanup(cleanup)
	return pool, cleanup
}

// startContainer runs postgres:16-alpine, applies the embedded
// partman migrations, and stores the DSN for later NewPG calls.
func startContainer() {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	ctr, err := tcpg.Run(ctx,
		containerImage,
		tcpg.WithDatabase("partman"),
		tcpg.WithUsername("partman"),
		tcpg.WithPassword("partman"),
		tcpg.BasicWaitStrategies(),
	)
	if err != nil {
		containerErr = fmt.Errorf("run container: %w", err)
		return
	}

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		containerErr = fmt.Errorf("connection string: %w", err)
		return
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		containerErr = fmt.Errorf("initial connect: %w", err)
		return
	}
	defer pool.Close()

	for _, m := range partman.Migrations() {
		if _, err := pool.Exec(ctx, m.SQL); err != nil {
			containerErr = fmt.Errorf("apply migration %04d_%s: %w", m.Version, m.Name, err)
			return
		}
	}

	containerDSN = dsn
}

// resetPool empties the partman metadata tables, drops any user schemas
// created by the test, and closes the pool.
func resetPool(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := pool.Exec(ctx,
		"TRUNCATE partman.parent_tables, partman.tenants, partman.partitions RESTART IDENTITY CASCADE",
	); err != nil {
		t.Logf("truncate partman schema: %v", err)
	}

	rows, err := pool.Query(ctx, `
		SELECT nspname FROM pg_namespace
		WHERE nspname NOT IN ('pg_catalog', 'information_schema', 'pg_toast', 'public', 'partman')
		  AND nspname NOT LIKE 'pg_temp_%'
		  AND nspname NOT LIKE 'pg_toast_temp_%'
	`)
	if err != nil {
		t.Logf("list user schemas: %v", err)
	} else {
		var names []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				t.Logf("scan schema name: %v", err)
				continue
			}
			names = append(names, name)
		}
		rows.Close()
		for _, name := range names {
			ident := pgx.Identifier{name}.Sanitize()
			if _, err := pool.Exec(ctx, "DROP SCHEMA "+ident+" CASCADE"); err != nil {
				t.Logf("drop schema %s: %v", name, err)
			}
		}
	}

	pool.Close()
}
