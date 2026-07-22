package importer

import (
	"fmt"

	"github.com/jirevwe/go_partman/internal/naming"
)

// intervalKind mirrors provisioner's private kind enum. Kept local to
// avoid an internal→internal dependency; the label set is fixed at
// four values so duplication is cheap.
type intervalKind int

const (
	intervalUnknown intervalKind = iota
	intervalHourly
	intervalDaily
	intervalWeekly
	intervalMonthly
)

// intervalKindFromLabel translates the string stored in
// partman.parent_tables.partition_interval into a kind. Anything the
// four Registry labels ("hourly"/"daily"/"weekly"/"monthly") do not
// cover is an error.
func intervalKindFromLabel(label string) (intervalKind, error) {
	switch label {
	case "hourly":
		return intervalHourly, nil
	case "daily":
		return intervalDaily, nil
	case "weekly":
		return intervalWeekly, nil
	case "monthly":
		return intervalMonthly, nil
	default:
		return intervalUnknown, fmt.Errorf("importer: unknown partition_interval label %q", label)
	}
}

// intervalMatches returns true when the (from, to) bounds are exactly
// one canonical period apart for the given kind. Monthly uses calendar
// arithmetic (from → from + 1 calendar month) to match how Provisioner
// creates monthly partitions.
func intervalMatches(k intervalKind, b naming.Bounds) bool {
	switch k {
	case intervalHourly:
		return b.To.Sub(b.From) == naming.PartitionHourInterval
	case intervalDaily:
		return b.To.Sub(b.From) == naming.PartitionDayInterval
	case intervalWeekly:
		return b.To.Sub(b.From) == naming.PartitionWeekInterval
	case intervalMonthly:
		return b.From.AddDate(0, 1, 0).Equal(b.To)
	default:
		return false
	}
}
