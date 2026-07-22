package retention

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// computeCutoff returns the timestamp before which partitions are
// considered expired: now minus the parent's retention_period. The
// pgtype.Interval representation carries Months, Days, and
// Microseconds; each component is subtracted with the appropriate
// calendar-aware helper so an interval of "1 month" removes one
// calendar month, not 30 days.
//
// A zero interval returns now unchanged. Retention is not applied
// when retention_period is zero — the ListExpiredPartitions filter
// (`partition_bounds_to <= now`) would still catch every past
// partition, so callers must set a non-zero retention_period.
func computeCutoff(now time.Time, iv pgtype.Interval) time.Time {
	if !iv.Valid {
		return now
	}
	out := now
	if iv.Months != 0 {
		out = out.AddDate(0, int(-iv.Months), 0)
	}
	if iv.Days != 0 {
		out = out.AddDate(0, 0, int(-iv.Days))
	}
	if iv.Microseconds != 0 {
		out = out.Add(-time.Duration(iv.Microseconds) * time.Microsecond)
	}
	return out
}
