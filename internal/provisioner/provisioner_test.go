package provisioner

import (
	"strings"
	"testing"
	"time"

	"github.com/jirevwe/go_partman/internal/naming"
)

func TestNextBoundsUTC_Hourly(t *testing.T) {
	now := time.Date(2026, 3, 15, 13, 37, 42, 0, time.UTC)
	got, err := NextBoundsUTC(now, naming.PartitionHourInterval, 3)
	if err != nil {
		t.Fatalf("NextBoundsUTC: %v", err)
	}
	want := []naming.Bounds{
		{From: time.Date(2026, 3, 15, 13, 0, 0, 0, time.UTC), To: time.Date(2026, 3, 15, 14, 0, 0, 0, time.UTC)},
		{From: time.Date(2026, 3, 15, 14, 0, 0, 0, time.UTC), To: time.Date(2026, 3, 15, 15, 0, 0, 0, time.UTC)},
		{From: time.Date(2026, 3, 15, 15, 0, 0, 0, time.UTC), To: time.Date(2026, 3, 15, 16, 0, 0, 0, time.UTC)},
	}
	assertBoundsEqual(t, want, got)
}

func TestNextBoundsUTC_Daily(t *testing.T) {
	now := time.Date(2026, 1, 15, 13, 37, 0, 0, time.UTC)
	got, err := NextBoundsUTC(now, naming.PartitionDayInterval, 3)
	if err != nil {
		t.Fatalf("NextBoundsUTC: %v", err)
	}
	want := []naming.Bounds{
		{From: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 1, 16, 0, 0, 0, 0, time.UTC)},
		{From: time.Date(2026, 1, 16, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 1, 17, 0, 0, 0, 0, time.UTC)},
		{From: time.Date(2026, 1, 17, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 1, 18, 0, 0, 0, 0, time.UTC)},
	}
	assertBoundsEqual(t, want, got)
}

func TestNextBoundsUTC_Weekly_SundayInputFloorsToMonday(t *testing.T) {
	// 2026-01-04 is a Sunday. Floor should land on Monday 2025-12-29.
	now := time.Date(2026, 1, 4, 12, 0, 0, 0, time.UTC)
	got, err := NextBoundsUTC(now, naming.PartitionWeekInterval, 2)
	if err != nil {
		t.Fatalf("NextBoundsUTC: %v", err)
	}
	want := []naming.Bounds{
		{From: time.Date(2025, 12, 29, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)},
		{From: time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 1, 12, 0, 0, 0, 0, time.UTC)},
	}
	assertBoundsEqual(t, want, got)
}

func TestNextBoundsUTC_Weekly_MondayInputStays(t *testing.T) {
	// 2026-01-05 is a Monday. Floor should be itself.
	now := time.Date(2026, 1, 5, 9, 30, 0, 0, time.UTC)
	got, err := NextBoundsUTC(now, naming.PartitionWeekInterval, 1)
	if err != nil {
		t.Fatalf("NextBoundsUTC: %v", err)
	}
	want := []naming.Bounds{
		{From: time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 1, 12, 0, 0, 0, 0, time.UTC)},
	}
	assertBoundsEqual(t, want, got)
}

func TestNextBoundsUTC_Monthly_CalendarBoundary(t *testing.T) {
	// The ADR acceptance case: input 2026-01-31T23:59:59Z must produce
	// January, February, March, April partitions with correct 1st-of-
	// month bounds (NOT 30-day fixed windows).
	now := time.Date(2026, 1, 31, 23, 59, 59, 0, time.UTC)
	got, err := NextBoundsUTC(now, naming.PartitionMonthInterval, 4)
	if err != nil {
		t.Fatalf("NextBoundsUTC: %v", err)
	}
	want := []naming.Bounds{
		{From: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)},
		{From: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)},
		{From: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)},
		{From: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)},
	}
	assertBoundsEqual(t, want, got)
}

func TestNextBoundsUTC_Monthly_YearRollover(t *testing.T) {
	// 2026-12-15 should produce December, January (2027), February
	// (2027) partitions.
	now := time.Date(2026, 12, 15, 6, 0, 0, 0, time.UTC)
	got, err := NextBoundsUTC(now, naming.PartitionMonthInterval, 3)
	if err != nil {
		t.Fatalf("NextBoundsUTC: %v", err)
	}
	want := []naming.Bounds{
		{From: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)},
		{From: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2027, 2, 1, 0, 0, 0, 0, time.UTC)},
		{From: time.Date(2027, 2, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)},
	}
	assertBoundsEqual(t, want, got)
}

func TestNextBoundsUTC_UnsupportedDurationReturnsError(t *testing.T) {
	_, err := NextBoundsUTC(time.Now(), 17*time.Minute, 2)
	if err == nil {
		t.Fatal("want error for unsupported interval, got nil")
	}
}

func TestNextBoundsUTC_NonUTCInputStillFloorsInUTC(t *testing.T) {
	// 2026-03-15 20:00 local (-08:00) is 2026-03-16 04:00 UTC.
	// Daily floor should be 2026-03-16 00:00 UTC.
	loc := time.FixedZone("PST", -8*60*60)
	now := time.Date(2026, 3, 15, 20, 0, 0, 0, loc)
	got, err := NextBoundsUTC(now, naming.PartitionDayInterval, 1)
	if err != nil {
		t.Fatalf("NextBoundsUTC: %v", err)
	}
	if !got[0].From.Equal(time.Date(2026, 3, 16, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("floor did not shift to UTC day boundary: got %v", got[0].From)
	}
}

func TestParseIntervalLabel(t *testing.T) {
	cases := []struct {
		in   string
		want kind
		bad  bool
	}{
		{"hourly", kindHourly, false},
		{"daily", kindDaily, false},
		{"weekly", kindWeekly, false},
		{"monthly", kindMonthly, false},
		{"", kindUnknown, true},
		{"HOURLY", kindUnknown, true},
		{"720h0m0s", kindUnknown, true},
	}
	for _, tc := range cases {
		got, err := parseIntervalLabel(tc.in)
		if tc.bad {
			if err == nil {
				t.Errorf("parseIntervalLabel(%q) want error, got kind=%d", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseIntervalLabel(%q) error: %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("parseIntervalLabel(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestBuildBoundedPartitionDDL_NoTenant(t *testing.T) {
	b := naming.Bounds{
		From: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
	}
	got := buildBoundedPartitionDDL("app", "events", "app", "events_20260101", "", b)
	want := `CREATE TABLE IF NOT EXISTS "app"."events_20260101" PARTITION OF "app"."events" FOR VALUES FROM (TIMESTAMPTZ '2026-01-01T00:00:00Z') TO (TIMESTAMPTZ '2026-02-01T00:00:00Z')`
	if got != want {
		t.Errorf("DDL mismatch:\ngot:  %s\nwant: %s", got, want)
	}
}

func TestBuildBoundedPartitionDDL_WithTenant(t *testing.T) {
	b := naming.Bounds{
		From: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
	}
	got := buildBoundedPartitionDDL("app", "events", "app", "events_TENANT1_20260101", "TENANT1", b)
	want := `CREATE TABLE IF NOT EXISTS "app"."events_TENANT1_20260101" PARTITION OF "app"."events" FOR VALUES FROM ('TENANT1', TIMESTAMPTZ '2026-01-01T00:00:00Z') TO ('TENANT1', TIMESTAMPTZ '2026-02-01T00:00:00Z')`
	if got != want {
		t.Errorf("DDL mismatch:\ngot:  %s\nwant: %s", got, want)
	}
}

func TestBuildDefaultPartitionDDL(t *testing.T) {
	got := buildDefaultPartitionDDL("app", "events", "app", "events_default")
	want := `CREATE TABLE IF NOT EXISTS "app"."events_default" PARTITION OF "app"."events" DEFAULT`
	if got != want {
		t.Errorf("DDL mismatch:\ngot:  %s\nwant: %s", got, want)
	}
}

func TestPqQuoteLiteral_EscapesQuotes(t *testing.T) {
	got := pqQuoteLiteral("a'b")
	want := "'a''b'"
	if got != want {
		t.Errorf("pqQuoteLiteral = %q, want %q", got, want)
	}
}

func TestPqQuoteLiteral_QuotesWithIdentifierSanitize(t *testing.T) {
	// Sanity check: the sanitize output for a schema with a quote is
	// well-formed enough that we can safely compose our DDL string.
	// We're not testing pgx.Identifier itself, just that our DDL
	// builder doesn't break when the schema has one.
	b := naming.Bounds{
		From: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
	}
	got := buildBoundedPartitionDDL(`we"ird`, "events", "app", "child", "", b)
	if !strings.Contains(got, `"we""ird"`) {
		t.Errorf("expected sanitized schema in DDL: %s", got)
	}
}

func TestSplitFQ(t *testing.T) {
	s, n, ok := splitFQ("app.events_20260101")
	if !ok || s != "app" || n != "events_20260101" {
		t.Errorf("splitFQ mismatch: %q %q %v", s, n, ok)
	}
	if _, _, ok := splitFQ("no_dot"); ok {
		t.Error("splitFQ should reject inputs without a dot")
	}
}

func assertBoundsEqual(t *testing.T, want, got []naming.Bounds) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("len mismatch: want %d got %d\nwant=%v\ngot =%v", len(want), len(got), want, got)
	}
	for i := range want {
		if !want[i].From.Equal(got[i].From) || !want[i].To.Equal(got[i].To) {
			t.Errorf("bounds[%d] mismatch:\n want %v\n got  %v", i, want[i], got[i])
		}
	}
}
