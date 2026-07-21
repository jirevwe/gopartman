package go_partman

// Migrations returns the SQL migrations in ascending version order.
// The consumer applies them at startup with any migration runner.
//
// ADR-0002 replaces this stub with a []Migration return value once
// the versioned migration files exist. Keep the exported name stable
// so downstream epics can code against it.
func Migrations() [][]byte {
	return nil
}
