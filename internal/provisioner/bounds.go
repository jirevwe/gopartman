package provisioner

import (
	"fmt"
	"time"

	"github.com/jirevwe/go_partman/internal/naming"
)

// kind is the internal enum of supported partition intervals. It maps
// 1:1 to the canonical string labels stored in
// partman.parent_tables.partition_interval.
type kind int

const (
	kindUnknown kind = iota
	kindHourly
	kindDaily
	kindWeekly
	kindMonthly
)

const (
	labelHourly  = "hourly"
	labelDaily   = "daily"
	labelWeekly  = "weekly"
	labelMonthly = "monthly"
)

// parseIntervalLabel resolves a canonical string label back to a kind.
func parseIntervalLabel(label string) (kind, error) {
	switch label {
	case labelHourly:
		return kindHourly, nil
	case labelDaily:
		return kindDaily, nil
	case labelWeekly:
		return kindWeekly, nil
	case labelMonthly:
		return kindMonthly, nil
	default:
		return kindUnknown, fmt.Errorf("provisioner: unknown partition_interval label %q", label)
	}
}

// kindFromDuration maps the four public interval sentinel constants to
// their kind. PartitionMonthInterval is a sentinel (its numeric value
// is not a real month) — the exact-value comparison is intentional.
func kindFromDuration(d time.Duration) (kind, error) {
	switch d {
	case naming.PartitionHourInterval:
		return kindHourly, nil
	case naming.PartitionDayInterval:
		return kindDaily, nil
	case naming.PartitionWeekInterval:
		return kindWeekly, nil
	case naming.PartitionMonthInterval:
		return kindMonthly, nil
	default:
		return kindUnknown, fmt.Errorf("provisioner: unsupported interval duration %s", d)
	}
}

// NextBoundsUTC returns `count` contiguous half-open [From, To) bounds
// starting at the current period's floor for `now`. All arithmetic is
// UTC. Interval is one of the four public partman.PartitionXInterval
// constants; PartitionMonthInterval is treated as a calendar-month
// sentinel, not a fixed 30-day window.
func NextBoundsUTC(now time.Time, interval time.Duration, count int) ([]naming.Bounds, error) {
	if count <= 0 {
		return nil, nil
	}
	k, err := kindFromDuration(interval)
	if err != nil {
		return nil, err
	}
	out := make([]naming.Bounds, 0, count)
	from := floorUTC(now, k)
	for range count {
		to := nextUTC(from, k)
		out = append(out, naming.Bounds{From: from, To: to})
		from = to
	}
	return out, nil
}

// floorUTC returns the start of the period that contains t, in UTC.
func floorUTC(t time.Time, k kind) time.Time {
	t = t.UTC()
	switch k {
	case kindHourly:
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, time.UTC)
	case kindDaily:
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	case kindWeekly:
		// ISO week: shift Sunday(0) → 6, so Monday(1) → 0.
		daysSinceMonday := (int(t.Weekday()) + 6) % 7
		day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		return day.AddDate(0, 0, -daysSinceMonday)
	case kindMonthly:
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	default:
		return t
	}
}

// nextUTC returns the start of the period immediately after `from`.
// `from` must already be a floor; this function does not defensively
// re-floor.
func nextUTC(from time.Time, k kind) time.Time {
	switch k {
	case kindHourly:
		return from.Add(time.Hour)
	case kindDaily:
		return from.AddDate(0, 0, 1)
	case kindWeekly:
		return from.AddDate(0, 0, 7)
	case kindMonthly:
		return from.AddDate(0, 1, 0)
	default:
		return from
	}
}

// canonicalIntervalFor returns the duration constant that corresponds
// to a kind. Used when constructing a naming.TableName that carries an
// Interval field.
func canonicalIntervalFor(k kind) time.Duration {
	switch k {
	case kindHourly:
		return naming.PartitionHourInterval
	case kindDaily:
		return naming.PartitionDayInterval
	case kindWeekly:
		return naming.PartitionWeekInterval
	case kindMonthly:
		return naming.PartitionMonthInterval
	default:
		return 0
	}
}
