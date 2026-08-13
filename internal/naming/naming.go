// Package naming holds the partition-name grammar. It lives in
// internal/ so both the root package and internal/provisioner can share
// the same Build/Parse logic without a circular import. The root
// package re-exports TableName and Bounds via type aliases to preserve
// the public API described in ADR-0001.
package naming

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// DateNoHyphens is the layout used in bounded partition suffixes.
const DateNoHyphens = "20060102"

// Partition-interval sentinel constants. See root types.go for the
// user-facing documentation on why PartitionMonthInterval is a sentinel
// (its numeric value is not a real month; Provisioner recognizes the
// exact value and switches to calendar-month arithmetic).
const (
	PartitionMonthInterval = time.Hour * 24 * 30
	PartitionWeekInterval  = time.Hour * 24 * 7
	PartitionDayInterval   = time.Hour * 24
	PartitionHourInterval  = time.Hour
)

// Bounds is the half-open time range [From, To). From is included, To
// is excluded. Bounds match PostgreSQL range semantics.
type Bounds struct {
	From, To time.Time
}

// TableName represents the components of a partition table name.
//
// The fully qualified form is:
//
//	{schema}.{parent}[_TENANT]_{YYYYMMDD|default}
type TableName struct {
	SchemaName string
	ParentName string
	Bounds     Bounds
	TenantId   string // optional
	IsDefault  bool
	Interval   time.Duration // optional; used by callers that carry the parent's interval
}

var alphaNumericRegex = regexp.MustCompile(`^\w+$`)

// Build returns the fully qualified partition table name. The tenant
// id is upper-cased. Schema, parent, and tenant must match `^\w+$`.
func (tn TableName) Build() (string, error) {
	if !alphaNumericRegex.MatchString(tn.SchemaName) {
		return "", fmt.Errorf("schema name contains invalid characters")
	}
	if !alphaNumericRegex.MatchString(tn.ParentName) {
		return "", fmt.Errorf("parent name contains invalid characters")
	}

	var b strings.Builder
	b.WriteString(tn.SchemaName)
	b.WriteString(".")
	b.WriteString(tn.ParentName)

	if len(tn.TenantId) > 0 {
		if !alphaNumericRegex.MatchString(tn.TenantId) {
			return "", fmt.Errorf("tenant id contains invalid characters")
		}
		b.WriteString("_")
		b.WriteString(strings.ToUpper(tn.TenantId))
	}

	if tn.IsDefault {
		b.WriteString("_default")
	} else {
		b.WriteString("_")
		b.WriteString(tn.Bounds.From.Format(DateNoHyphens))
	}

	return b.String(), nil
}

// Parse is the inverse of Build. It splits a fully qualified partition
// table name into its components, given the name of the parent table the
// partition belongs to. Parse leaves Bounds.To and Interval as zero
// values; callers that need those must compute them from the parent's
// registered interval.
//
// The parent name is required because a tenant segment is otherwise
// indistinguishable from the last word of a multi-word parent. In
// "public.order_items_20060102", "items" could be either, and no property
// of the string itself decides which. Callers always know the parent they
// are reconciling, so ask for it rather than guess.
//
// Comparison of the parent segment is case-insensitive, and the tenant is
// returned as written. PostgreSQL folds unquoted identifiers to lower
// case, so a partition created by DDL outside this package can carry a
// tenant that Build would have upper-cased. Refusing to read those is
// what makes them un-adoptable, and an un-adopted partition is one
// retention never drops.
func (TableName) Parse(fqName, parentName string) (TableName, error) {
	dot := strings.Index(fqName, ".")
	if dot < 0 {
		return TableName{}, fmt.Errorf("fqName missing schema separator: %q", fqName)
	}
	schema := fqName[:dot]
	rest := fqName[dot+1:]

	if !alphaNumericRegex.MatchString(schema) {
		return TableName{}, fmt.Errorf("schema name contains invalid characters")
	}
	if !alphaNumericRegex.MatchString(parentName) {
		return TableName{}, fmt.Errorf("parent name contains invalid characters")
	}

	lastUnderscore := strings.LastIndex(rest, "_")
	if lastUnderscore < 0 {
		return TableName{}, fmt.Errorf("fqName missing suffix: %q", fqName)
	}
	suffix := rest[lastUnderscore+1:]
	remainder := rest[:lastUnderscore]

	tn := TableName{SchemaName: schema, ParentName: parentName}

	switch {
	case suffix == "default":
		tn.IsDefault = true
	case len(suffix) == 8:
		t, err := time.ParseInLocation(DateNoHyphens, suffix, time.UTC)
		if err != nil {
			return TableName{}, fmt.Errorf("invalid date suffix %q: %w", suffix, err)
		}
		tn.Bounds = Bounds{From: t}
	default:
		return TableName{}, fmt.Errorf("suffix must be \"default\" or YYYYMMDD, got %q", suffix)
	}

	// Anything between the parent name and the suffix is the tenant.
	switch {
	case strings.EqualFold(remainder, parentName):
		// No tenant segment.
	case len(remainder) > len(parentName)+1 &&
		strings.EqualFold(remainder[:len(parentName)], parentName) &&
		remainder[len(parentName)] == '_':
		tn.TenantId = remainder[len(parentName)+1:]
		if !alphaNumericRegex.MatchString(tn.TenantId) {
			return TableName{}, fmt.Errorf("tenant id contains invalid characters")
		}
	default:
		return TableName{}, fmt.Errorf("name %q does not belong to parent %q", fqName, parentName)
	}

	return tn, nil
}

// PartitionIntervalLabel maps a supported partition-interval constant
// to its canonical string label used in
// partman.parent_tables.partition_interval. Registry (ADR-0005) writes
// the label; Provisioner reads it back.
//
// Only the four exported interval constants are accepted. Any other
// duration returns an error.
func PartitionIntervalLabel(d time.Duration) (string, error) {
	switch d {
	case PartitionHourInterval:
		return "hourly", nil
	case PartitionDayInterval:
		return "daily", nil
	case PartitionWeekInterval:
		return "weekly", nil
	case PartitionMonthInterval:
		return "monthly", nil
	default:
		return "", fmt.Errorf("partman: unsupported partition interval %s; use one of the PartitionXInterval constants", d)
	}
}
