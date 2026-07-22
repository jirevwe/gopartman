package importer

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jirevwe/go_partman/internal/naming"
)

// parsedBound is the semantic form of a child's pg_get_expr(relpartbound)
// output. IsDefault=true means the child is the DEFAULT partition and
// Bounds/TenantId are zero. TenantId is upper-cased so it aligns with
// what TableName.Build writes and TableName.Parse extracts.
type parsedBound struct {
	IsDefault bool
	TenantId  string
	Bounds    naming.Bounds
}

var boundedRE = regexp.MustCompile(`^FOR VALUES FROM \((.*)\) TO \((.*)\)$`)

// pgTimestampLayouts covers the shapes pg_get_expr may emit for a
// timestamptz literal. PG normalizes to "YYYY-MM-DD HH:MM:SS±HH" by
// default; RFC3339 is a safety net for outputs that include minutes on
// the offset or a "T" separator (some client encodings do this).
var pgTimestampLayouts = []string{
	"2006-01-02 15:04:05-07",
	"2006-01-02 15:04:05-0700",
	"2006-01-02 15:04:05.999999-07",
	"2006-01-02 15:04:05.999999-0700",
	time.RFC3339,
	time.RFC3339Nano,
}

// parseBoundExpr classifies the output of pg_get_expr(relpartbound). It
// handles three cases:
//
//   - "DEFAULT"                                                    → default
//   - "FOR VALUES FROM ('<ts>') TO ('<ts>')"                       → bounded, no tenant
//   - "FOR VALUES FROM ('<tenant>', '<ts>') TO ('<tenant>', '<ts>')" → bounded, tenant
//
// Any other shape returns an error. Callers use the error text as a
// SkippedPartition.Reason.
func parseBoundExpr(expr string) (parsedBound, error) {
	expr = strings.TrimSpace(expr)
	if expr == "DEFAULT" {
		return parsedBound{IsDefault: true}, nil
	}

	m := boundedRE.FindStringSubmatch(expr)
	if m == nil {
		return parsedBound{}, fmt.Errorf("bound expression %q: unrecognized shape", expr)
	}
	fromParts, err := splitTuple(m[1])
	if err != nil {
		return parsedBound{}, fmt.Errorf("bound FROM: %w", err)
	}
	toParts, err := splitTuple(m[2])
	if err != nil {
		return parsedBound{}, fmt.Errorf("bound TO: %w", err)
	}
	if len(fromParts) != len(toParts) {
		return parsedBound{}, fmt.Errorf("bound FROM has %d parts, TO has %d", len(fromParts), len(toParts))
	}

	switch len(fromParts) {
	case 1:
		fromTS, err := parsePGTimestamp(fromParts[0])
		if err != nil {
			return parsedBound{}, fmt.Errorf("bound FROM timestamp: %w", err)
		}
		toTS, err := parsePGTimestamp(toParts[0])
		if err != nil {
			return parsedBound{}, fmt.Errorf("bound TO timestamp: %w", err)
		}
		return parsedBound{Bounds: naming.Bounds{From: fromTS, To: toTS}}, nil
	case 2:
		fromTenant, err := unquote(fromParts[0])
		if err != nil {
			return parsedBound{}, fmt.Errorf("bound FROM tenant: %w", err)
		}
		toTenant, err := unquote(toParts[0])
		if err != nil {
			return parsedBound{}, fmt.Errorf("bound TO tenant: %w", err)
		}
		if fromTenant != toTenant {
			return parsedBound{}, fmt.Errorf("bound tenant mismatch between FROM (%q) and TO (%q)", fromTenant, toTenant)
		}
		fromTS, err := parsePGTimestamp(fromParts[1])
		if err != nil {
			return parsedBound{}, fmt.Errorf("bound FROM timestamp: %w", err)
		}
		toTS, err := parsePGTimestamp(toParts[1])
		if err != nil {
			return parsedBound{}, fmt.Errorf("bound TO timestamp: %w", err)
		}
		return parsedBound{
			TenantId: strings.ToUpper(fromTenant),
			Bounds:   naming.Bounds{From: fromTS, To: toTS},
		}, nil
	default:
		return parsedBound{}, fmt.Errorf("bound tuple has %d parts; expected 1 or 2", len(fromParts))
	}
}

// splitTuple splits a tuple body (contents between the parentheses of
// FROM/TO) on comma-outside-quotes. Whitespace around each part is
// trimmed. Empty parts and unterminated quotes return an error.
func splitTuple(body string) ([]string, error) {
	var parts []string
	var cur strings.Builder
	inQuote := false
	for i := 0; i < len(body); i++ {
		c := body[i]
		switch {
		case c == '\'':
			// SQL escapes '' inside a quoted literal. Fold it back to a
			// single quote and stay in the quoted state.
			if inQuote && i+1 < len(body) && body[i+1] == '\'' {
				cur.WriteByte('\'')
				i++
				continue
			}
			inQuote = !inQuote
			cur.WriteByte(c)
		case c == ',' && !inQuote:
			parts = append(parts, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	if inQuote {
		return nil, fmt.Errorf("unterminated quoted literal in %q", body)
	}
	parts = append(parts, strings.TrimSpace(cur.String()))
	if slices.Contains(parts, "") {
		return nil, fmt.Errorf("empty tuple element in %q", body)
	}
	return parts, nil
}

// unquote strips a leading and trailing single quote and folds any
// escaped ” back to a single '. The trailing "::type" cast that PG may
// append is stripped first.
func unquote(v string) (string, error) {
	v = stripCast(v)
	if len(v) < 2 || v[0] != '\'' || v[len(v)-1] != '\'' {
		return "", fmt.Errorf("value %q is not a single-quoted literal", v)
	}
	inner := v[1 : len(v)-1]
	return strings.ReplaceAll(inner, "''", "'"), nil
}

// stripCast removes any trailing "::typename" cast a PG emitter may
// have added, e.g. "'2026-01-01'::timestamptz".
func stripCast(v string) string {
	if idx := strings.LastIndex(v, "::"); idx > 0 {
		return v[:idx]
	}
	return v
}

// parsePGTimestamp accepts either a quoted literal or a bare literal
// and returns the parsed UTC time. All candidate layouts in
// pgTimestampLayouts are tried; if none matches, the error names the
// input for debuggability.
func parsePGTimestamp(v string) (time.Time, error) {
	raw, err := unquote(v)
	if err != nil {
		// Fall back: v may already be bare (no quotes) in exotic
		// dumps. Strip cast just in case.
		raw = stripCast(v)
	}
	for _, layout := range pgTimestampLayouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("timestamp %q does not match any known PG layout", raw)
}
