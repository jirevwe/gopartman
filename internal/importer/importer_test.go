package importer

import (
	"testing"
	"time"

	"github.com/jirevwe/gopartman/internal/naming"
)

func TestCompareNameAndBound_Aligned(t *testing.T) {
	from := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 4, 16, 0, 0, 0, 0, time.UTC)
	name := naming.TableName{
		SchemaName: "public",
		ParentName: "events",
		Bounds:     naming.Bounds{From: from},
	}
	bound := parsedBound{Bounds: naming.Bounds{From: from, To: to}}
	if got := compareNameAndBound(name, bound); got != "" {
		t.Errorf("reason = %q, want empty (aligned)", got)
	}
}

func TestCompareNameAndBound_TenantAligned(t *testing.T) {
	from := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)
	name := naming.TableName{
		SchemaName: "public",
		ParentName: "events",
		TenantId:   "ABC",
		Bounds:     naming.Bounds{From: from},
	}
	bound := parsedBound{
		TenantId: "ABC",
		Bounds:   naming.Bounds{From: from, To: from.AddDate(0, 0, 1)},
	}
	if got := compareNameAndBound(name, bound); got != "" {
		t.Errorf("reason = %q, want empty (aligned)", got)
	}
}

func TestCompareNameAndBound_TenantMismatch(t *testing.T) {
	from := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)
	name := naming.TableName{TenantId: "ABC", Bounds: naming.Bounds{From: from}}
	bound := parsedBound{TenantId: "XYZ", Bounds: naming.Bounds{From: from}}
	got := compareNameAndBound(name, bound)
	if got == "" {
		t.Fatal("expected drift reason, got empty")
	}
}

func TestCompareNameAndBound_DefaultVsBounded(t *testing.T) {
	from := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)
	name := naming.TableName{IsDefault: true}
	bound := parsedBound{Bounds: naming.Bounds{From: from, To: from.AddDate(0, 0, 1)}}
	got := compareNameAndBound(name, bound)
	if got == "" {
		t.Fatal("expected drift reason, got empty")
	}
}

func TestCompareNameAndBound_BoundedVsDefault(t *testing.T) {
	from := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)
	name := naming.TableName{Bounds: naming.Bounds{From: from}}
	bound := parsedBound{IsDefault: true}
	got := compareNameAndBound(name, bound)
	if got == "" {
		t.Fatal("expected drift reason, got empty")
	}
}

func TestCompareNameAndBound_FromMismatch(t *testing.T) {
	nameFrom := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)
	boundFrom := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)
	name := naming.TableName{Bounds: naming.Bounds{From: nameFrom}}
	bound := parsedBound{Bounds: naming.Bounds{From: boundFrom, To: boundFrom.AddDate(0, 0, 1)}}
	got := compareNameAndBound(name, bound)
	if got == "" {
		t.Fatal("expected drift reason, got empty")
	}
}

func TestCompareNameAndBound_BothDefault(t *testing.T) {
	name := naming.TableName{IsDefault: true}
	bound := parsedBound{IsDefault: true}
	if got := compareNameAndBound(name, bound); got != "" {
		t.Errorf("reason = %q, want empty (both default)", got)
	}
}

func TestIntervalMatches(t *testing.T) {
	tests := []struct {
		name string
		k    intervalKind
		from time.Time
		to   time.Time
		want bool
	}{
		{
			name: "daily aligned",
			k:    intervalDaily,
			from: time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC),
			to:   time.Date(2026, 4, 16, 0, 0, 0, 0, time.UTC),
			want: true,
		},
		{
			name: "daily off by an hour",
			k:    intervalDaily,
			from: time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC),
			to:   time.Date(2026, 4, 16, 1, 0, 0, 0, time.UTC),
			want: false,
		},
		{
			name: "hourly aligned",
			k:    intervalHourly,
			from: time.Date(2026, 4, 15, 5, 0, 0, 0, time.UTC),
			to:   time.Date(2026, 4, 15, 6, 0, 0, 0, time.UTC),
			want: true,
		},
		{
			name: "weekly aligned",
			k:    intervalWeekly,
			from: time.Date(2026, 4, 13, 0, 0, 0, 0, time.UTC),
			to:   time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC),
			want: true,
		},
		{
			name: "monthly aligned (calendar arithmetic)",
			k:    intervalMonthly,
			from: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
			to:   time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
			want: true,
		},
		{
			name: "monthly across a Feb boundary",
			k:    intervalMonthly,
			from: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
			to:   time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
			want: true,
		},
		{
			name: "monthly but only 30 days apart across Feb",
			k:    intervalMonthly,
			from: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
			to:   time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC),
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := intervalMatches(tc.k, naming.Bounds{From: tc.from, To: tc.to})
			if got != tc.want {
				t.Errorf("intervalMatches = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIntervalKindFromLabel(t *testing.T) {
	tests := []struct {
		in   string
		want intervalKind
	}{
		{"hourly", intervalHourly},
		{"daily", intervalDaily},
		{"weekly", intervalWeekly},
		{"monthly", intervalMonthly},
	}
	for _, tc := range tests {
		got, err := intervalKindFromLabel(tc.in)
		if err != nil {
			t.Errorf("intervalKindFromLabel(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("intervalKindFromLabel(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
	if _, err := intervalKindFromLabel("nope"); err == nil {
		t.Error("expected error for unknown label")
	}
}
