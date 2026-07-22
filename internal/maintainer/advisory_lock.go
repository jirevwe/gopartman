package maintainer

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TryLock asks PostgreSQL for a session-scoped advisory lock keyed on
// hashtext(schema) and hashtext(table). It returns true when the lock
// is now held by the caller's connection. It returns false when
// another session holds the lock. The caller must hold conn for the
// duration of the work and call Unlock (or release the connection)
// when done.
//
// The lock key uses the two-int32 overload of pg_try_advisory_lock, as
// ADR-0007 specifies. hashtext returns int4 in PostgreSQL, so no cast
// is needed at the SQL layer.
func TryLock(ctx context.Context, conn *pgxpool.Conn, schema, table string) (bool, error) {
	var locked bool
	row := conn.QueryRow(ctx,
		`SELECT pg_try_advisory_lock(hashtext($1), hashtext($2))`,
		schema, table,
	)
	if err := row.Scan(&locked); err != nil {
		return false, fmt.Errorf("maintainer: pg_try_advisory_lock: %w", err)
	}
	return locked, nil
}

// Unlock releases the session-scoped advisory lock keyed on
// hashtext(schema) and hashtext(table). It returns an error only when
// the query itself fails. PostgreSQL returns false when the session
// held no lock for the key; this function reports that as an error to
// surface programmer mistakes.
func Unlock(ctx context.Context, conn *pgxpool.Conn, schema, table string) error {
	var released bool
	row := conn.QueryRow(ctx,
		`SELECT pg_advisory_unlock(hashtext($1), hashtext($2))`,
		schema, table,
	)
	if err := row.Scan(&released); err != nil {
		return fmt.Errorf("maintainer: pg_advisory_unlock: %w", err)
	}
	if !released {
		return fmt.Errorf("maintainer: pg_advisory_unlock returned false for %s.%s", schema, table)
	}
	return nil
}

// poolLocker is the production Locker. It acquires a session
// connection from the pool, calls TryLock, and hands back a release
// closure that Unlocks and releases the connection.
type poolLocker struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

func (l *poolLocker) TryLock(ctx context.Context, schema, table string) (bool, func(), error) {
	conn, err := l.pool.Acquire(ctx)
	if err != nil {
		return false, nil, fmt.Errorf("maintainer: acquire conn: %w", err)
	}
	locked, err := TryLock(ctx, conn, schema, table)
	if err != nil {
		conn.Release()
		return false, nil, err
	}
	if !locked {
		conn.Release()
		return false, nil, nil
	}
	release := func() {
		if err := Unlock(ctx, conn, schema, table); err != nil {
			l.logger.Warn("maintainer: Unlock failed",
				"parent", schema+"."+table,
				"err", err,
			)
		}
		conn.Release()
	}
	return true, release, nil
}
