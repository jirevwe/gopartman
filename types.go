package go_partman

import "github.com/jirevwe/go_partman/internal/naming"

// Partition-interval sentinels. Three of these are honest durations
// (hourly, daily, weekly). PartitionMonthInterval is NOT arithmetically
// a month — no month has exactly 720 hours. Provisioner recognizes the
// exact value of PartitionMonthInterval as a sentinel and switches to
// calendar-month arithmetic (1st-of-month to 1st-of-next-month, UTC).
// The value is preserved unchanged so existing callers do not break.
const (
	PartitionMonthInterval = naming.PartitionMonthInterval // sentinel; see doc
	PartitionWeekInterval  = naming.PartitionWeekInterval
	PartitionDayInterval   = naming.PartitionDayInterval
	PartitionHourInterval  = naming.PartitionHourInterval
)

// DateNoHyphens is the layout used in bounded partition suffixes.
const DateNoHyphens = naming.DateNoHyphens

// Bounds is the half-open time range [From, To). Aliased from
// internal/naming so both the root package and internal/provisioner
// share one type.
type Bounds = naming.Bounds

// Tenant represents a tenant configuration for a specific parent table.
type Tenant struct {
	// ParentName references the parent table this tenant belongs to.
	ParentName string

	// ParentSchema references the parent table schema.
	ParentSchema string

	// TenantId Tenant ID column value (e.g., 01J2V010NV1259CYWQEYQC8F35).
	TenantId string
}

// PartitionIntervalLabel maps a supported partition-interval constant
// to its canonical string label used in
// partman.parent_tables.partition_interval. Registry (ADR-0005) writes
// the label; Provisioner reads it back.
//
// Only the four exported interval constants are accepted. Any other
// duration returns an error.
var PartitionIntervalLabel = naming.PartitionIntervalLabel
