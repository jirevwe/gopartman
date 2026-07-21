package go_partman

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

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
	Interval   time.Duration // optional: pg_partman supports different intervals
}

var alphaNumericRegex = regexp.MustCompile(`^\w+$`)

// Build returns the fully qualified partition table name. The tenant id
// is upper-cased. Schema, parent, and tenant must match `^\w+$`.
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
// table name into its components. Parse leaves Bounds.To and Interval
// as zero values; callers that need those must compute them from the
// parent's registered interval.
//
// The heuristic for tenant detection: the segment right before the
// suffix is treated as a tenant only if it matches ^[A-Z0-9]+$, which
// matches Build's strings.ToUpper output.
func (TableName) Parse(fqName string) (TableName, error) {
	dot := strings.Index(fqName, ".")
	if dot < 0 {
		return TableName{}, fmt.Errorf("fqName missing schema separator: %q", fqName)
	}
	schema := fqName[:dot]
	rest := fqName[dot+1:]

	if !alphaNumericRegex.MatchString(schema) {
		return TableName{}, fmt.Errorf("schema name contains invalid characters")
	}

	lastUnderscore := strings.LastIndex(rest, "_")
	if lastUnderscore < 0 {
		return TableName{}, fmt.Errorf("fqName missing suffix: %q", fqName)
	}
	suffix := rest[lastUnderscore+1:]
	remainder := rest[:lastUnderscore]

	tn := TableName{SchemaName: schema}

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

	if tenantSep := strings.LastIndex(remainder, "_"); tenantSep >= 0 {
		candidate := remainder[tenantSep+1:]
		if isUpperAlphanumeric(candidate) {
			tn.TenantId = candidate
			tn.ParentName = remainder[:tenantSep]
		} else {
			tn.ParentName = remainder
		}
	} else {
		tn.ParentName = remainder
	}

	if !alphaNumericRegex.MatchString(tn.ParentName) {
		return TableName{}, fmt.Errorf("parent name contains invalid characters")
	}

	return tn, nil
}

func isUpperAlphanumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}
