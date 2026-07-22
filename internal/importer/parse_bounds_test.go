package importer

import (
	"strings"
	"testing"
	"time"
)

func TestParseBoundExpr_Default(t *testing.T) {
	got, err := parseBoundExpr("DEFAULT")
	if err != nil {
		t.Fatalf("parseBoundExpr(DEFAULT): %v", err)
	}
	if !got.IsDefault {
		t.Error("IsDefault = false, want true")
	}
	if got.TenantId != "" {
		t.Errorf("TenantId = %q, want empty", got.TenantId)
	}
	if !got.Bounds.From.IsZero() || !got.Bounds.To.IsZero() {
		t.Errorf("Bounds = %+v, want zero", got.Bounds)
	}
}

func TestParseBoundExpr_BoundedNoTenant(t *testing.T) {
	in := "FOR VALUES FROM ('2026-04-15 00:00:00+00') TO ('2026-04-16 00:00:00+00')"
	got, err := parseBoundExpr(in)
	if err != nil {
		t.Fatalf("parseBoundExpr: %v", err)
	}
	if got.IsDefault {
		t.Error("IsDefault = true, want false")
	}
	if got.TenantId != "" {
		t.Errorf("TenantId = %q, want empty", got.TenantId)
	}
	wantFrom := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)
	wantTo := time.Date(2026, 4, 16, 0, 0, 0, 0, time.UTC)
	if !got.Bounds.From.Equal(wantFrom) {
		t.Errorf("From = %s, want %s", got.Bounds.From, wantFrom)
	}
	if !got.Bounds.To.Equal(wantTo) {
		t.Errorf("To = %s, want %s", got.Bounds.To, wantTo)
	}
}

func TestParseBoundExpr_CompositeTenant(t *testing.T) {
	in := "FOR VALUES FROM ('ABC', '2026-04-15 00:00:00+00') TO ('ABC', '2026-04-16 00:00:00+00')"
	got, err := parseBoundExpr(in)
	if err != nil {
		t.Fatalf("parseBoundExpr: %v", err)
	}
	if got.TenantId != "ABC" {
		t.Errorf("TenantId = %q, want ABC", got.TenantId)
	}
	if got.Bounds.From.Year() != 2026 || got.Bounds.From.Day() != 15 {
		t.Errorf("From = %s", got.Bounds.From)
	}
}

func TestParseBoundExpr_CompositeTenant_LowercaseUppercased(t *testing.T) {
	in := "FOR VALUES FROM ('lowercase', '2026-04-15 00:00:00+00') TO ('lowercase', '2026-04-16 00:00:00+00')"
	got, err := parseBoundExpr(in)
	if err != nil {
		t.Fatalf("parseBoundExpr: %v", err)
	}
	if got.TenantId != "LOWERCASE" {
		t.Errorf("TenantId = %q, want LOWERCASE (uppercased for TableName.Parse parity)", got.TenantId)
	}
}

func TestParseBoundExpr_CompositeTenantMismatch(t *testing.T) {
	in := "FOR VALUES FROM ('AAA', '2026-04-15 00:00:00+00') TO ('BBB', '2026-04-16 00:00:00+00')"
	_, err := parseBoundExpr(in)
	if err == nil {
		t.Fatal("expected error for tenant mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "tenant mismatch") {
		t.Errorf("error should mention tenant mismatch, got %q", err.Error())
	}
}

func TestParseBoundExpr_TimestampFormats(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{
			name: "hour-offset short",
			in:   "FOR VALUES FROM ('2026-04-15 00:00:00+00') TO ('2026-04-16 00:00:00+00')",
		},
		{
			name: "hour-offset four-digit",
			in:   "FOR VALUES FROM ('2026-04-15 00:00:00+0000') TO ('2026-04-16 00:00:00+0000')",
		},
		{
			name: "with fractional seconds",
			in:   "FOR VALUES FROM ('2026-04-15 00:00:00.123456+00') TO ('2026-04-16 00:00:00.123456+00')",
		},
		{
			name: "negative offset",
			in:   "FOR VALUES FROM ('2026-04-15 05:00:00-05') TO ('2026-04-16 05:00:00-05')",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseBoundExpr(tc.in)
			if err != nil {
				t.Fatalf("parseBoundExpr: %v", err)
			}
			if got.Bounds.From.IsZero() || got.Bounds.To.IsZero() {
				t.Errorf("bounds zero: %+v", got.Bounds)
			}
		})
	}
}

func TestParseBoundExpr_TypeCastStripped(t *testing.T) {
	in := "FOR VALUES FROM ('2026-04-15 00:00:00+00'::timestamptz) TO ('2026-04-16 00:00:00+00'::timestamptz)"
	got, err := parseBoundExpr(in)
	if err != nil {
		t.Fatalf("parseBoundExpr: %v", err)
	}
	if got.Bounds.From.Year() != 2026 {
		t.Errorf("From = %s", got.Bounds.From)
	}
}

func TestParseBoundExpr_Malformed(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"garbage", "not a bound"},
		{"FROM without TO", "FOR VALUES FROM ('2026-04-15 00:00:00+00')"},
		{"three-part tuple", "FOR VALUES FROM ('a', 'b', '2026-04-15 00:00:00+00') TO ('a', 'b', '2026-04-16 00:00:00+00')"},
		{"asymmetric arity", "FOR VALUES FROM ('2026-04-15 00:00:00+00') TO ('X', '2026-04-16 00:00:00+00')"},
		{"unterminated quote", "FOR VALUES FROM ('unterminated) TO ('2026-04-16 00:00:00+00')"},
		{"bogus timestamp", "FOR VALUES FROM ('not-a-date') TO ('also-not-a-date')"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseBoundExpr(tc.in)
			if err == nil {
				t.Errorf("expected error for %q, got nil", tc.in)
			}
		})
	}
}

func TestSplitTuple_HandlesEscapedQuote(t *testing.T) {
	parts, err := splitTuple("'ten''ant', '2026-04-15 00:00:00+00'")
	if err != nil {
		t.Fatalf("splitTuple: %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("parts = %d, want 2", len(parts))
	}
	unq, err := unquote(parts[0])
	if err != nil {
		t.Fatalf("unquote: %v", err)
	}
	if unq != "ten'ant" {
		t.Errorf("unquote = %q, want ten'ant", unq)
	}
}
