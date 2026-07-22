package gopartman

import (
	"embed"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Migration is one versioned SQL file embedded in the library.
// Consumers apply Migrations() at startup with any migration runner.
// SQL must be executed as a single statement string, not split on ";" —
// some files contain dollar-quoted plpgsql function bodies.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// Migrations returns the embedded migrations in ascending version order.
//
// TODO(ADR-0003): the migrations-vs-schema smoke test lives in
// internal/testsupport/migrations_test.go and lands with the
// testcontainers harness from ADR-0003.
func Migrations() []Migration {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		panic(fmt.Errorf("partman: read embedded migrations: %w", err))
	}

	out := make([]Migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		version, label, err := parseMigrationFilename(name)
		if err != nil {
			panic(fmt.Errorf("partman: %w", err))
		}
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			panic(fmt.Errorf("partman: read %s: %w", name, err))
		}
		out = append(out, Migration{
			Version: version,
			Name:    label,
			SQL:     string(body),
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out
}

// parseMigrationFilename splits "NNNN_name.sql" into the integer
// version and the human-readable name.
func parseMigrationFilename(filename string) (int, string, error) {
	base := strings.TrimSuffix(filename, ".sql")
	underscore := strings.IndexByte(base, '_')
	if underscore <= 0 {
		return 0, "", fmt.Errorf("migration %q: expected NNNN_name.sql", filename)
	}
	version, err := strconv.Atoi(base[:underscore])
	if err != nil {
		return 0, "", fmt.Errorf("migration %q: version: %w", filename, err)
	}
	return version, base[underscore+1:], nil
}
