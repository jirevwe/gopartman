package retention

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/jirevwe/gopartman/internal/hooks"
)

func TestComputeCutoff_MicrosecondsOnly(t *testing.T) {
	now := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	iv := pgtype.Interval{
		Microseconds: int64(30 * 24 * time.Hour / time.Microsecond),
		Valid:        true,
	}
	got := computeCutoff(now, iv)
	want := time.Date(2026, 2, 13, 12, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("cutoff = %s, want %s", got, want)
	}
}

func TestComputeCutoff_DaysComponent(t *testing.T) {
	now := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	iv := pgtype.Interval{Days: 7, Valid: true}
	got := computeCutoff(now, iv)
	want := time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("cutoff = %s, want %s", got, want)
	}
}

func TestComputeCutoff_MonthsComponent(t *testing.T) {
	// One calendar month back from March 15 is February 15.
	now := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	iv := pgtype.Interval{Months: 1, Valid: true}
	got := computeCutoff(now, iv)
	want := time.Date(2026, 2, 15, 12, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("cutoff = %s, want %s", got, want)
	}
}

func TestComputeCutoff_InvalidInterval_ReturnsNow(t *testing.T) {
	now := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	got := computeCutoff(now, pgtype.Interval{Valid: false})
	if !got.Equal(now) {
		t.Errorf("cutoff = %s, want %s (unchanged)", got, now)
	}
}

func TestComputeCutoff_MixedComponents(t *testing.T) {
	// 1 month + 2 days + 1 hour back from March 15 12:00.
	now := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	iv := pgtype.Interval{
		Months:       1,
		Days:         2,
		Microseconds: int64(time.Hour / time.Microsecond),
		Valid:        true,
	}
	got := computeCutoff(now, iv)
	want := time.Date(2026, 2, 13, 11, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("cutoff = %s, want %s", got, want)
	}
}

func TestWithDryRun_SetsFlag(t *testing.T) {
	o := evalSweepOptions([]SweepOption{WithDryRun(true)})
	if !o.dryRun {
		t.Error("WithDryRun(true) did not set dryRun")
	}
}

func TestWithDryRun_DefaultsFalse(t *testing.T) {
	o := evalSweepOptions(nil)
	if o.dryRun {
		t.Error("default dryRun should be false")
	}
}

func TestSweepReport_ZeroValue(t *testing.T) {
	var r SweepReport
	if r.Considered != 0 {
		t.Errorf("Considered = %d, want 0", r.Considered)
	}
	if len(r.Dropped)+len(r.Detached)+len(r.Archived)+len(r.Skipped) != 0 {
		t.Error("zero report should have empty slices")
	}
	if r.DryRun {
		t.Error("zero report DryRun should be false")
	}
}

func TestBuildDropDDL_SanitizesIdentifier(t *testing.T) {
	got := buildDropDDL("myschema", "events_20260315")
	want := `DROP TABLE "myschema"."events_20260315" CASCADE`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildDropIfExistsDDL(t *testing.T) {
	got := buildDropIfExistsDDL("myschema", "events_20260315")
	want := `DROP TABLE IF EXISTS "myschema"."events_20260315" CASCADE`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildDetachDDL(t *testing.T) {
	got := buildDetachDDL("myschema", "events", "myschema", "events_20260315")
	want := `ALTER TABLE "myschema"."events" DETACH PARTITION "myschema"."events_20260315"`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildSetSchemaDDL(t *testing.T) {
	got := buildSetSchemaDDL("myschema", "events_20260315", "archive")
	want := `ALTER TABLE "myschema"."events_20260315" SET SCHEMA "archive"`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSplitFQ_Ok(t *testing.T) {
	s, n, ok := splitFQ("myschema.events_20260315")
	if !ok || s != "myschema" || n != "events_20260315" {
		t.Errorf("splitFQ = (%q, %q, %v)", s, n, ok)
	}
}

func TestSplitFQ_NoDot(t *testing.T) {
	if _, _, ok := splitFQ("events"); ok {
		t.Error("expected ok=false for missing dot")
	}
}

func TestDecisionLabel(t *testing.T) {
	cases := []struct {
		in   hooks.HookDecision
		want string
	}{
		{hooks.HookDrop, "drop"},
		{hooks.HookDetach, "detach"},
		{hooks.HookArchive, "archive"},
		{hooks.HookSkip, "skip"},
		{hooks.HookDecision(99), "unknown"},
	}
	for _, c := range cases {
		if got := decisionLabel(c.in); got != c.want {
			t.Errorf("decisionLabel(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}
