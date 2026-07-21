package go_partman

import "time"

const (
	PartitionMonthInterval = time.Hour * 24 * 30 // 30 days (1 month)
	PartitionWeekInterval  = time.Hour * 24 * 7
	PartitionDayInterval   = time.Hour * 24
	PartitionHourInterval  = time.Hour
)

const (
	DateNoHyphens = "20060102"
)

// Bounds is the half-open time range [From, To). From is included, To is
// excluded. Bounds match PostgreSQL range semantics.
type Bounds struct {
	From, To time.Time
}

// Tenant represents a tenant configuration for a specific parent table.
type Tenant struct {
	// ParentName references the parent table this tenant belongs to.
	ParentName string

	// ParentSchema references the parent table schema.
	ParentSchema string

	// TenantId Tenant ID column value (e.g., 01J2V010NV1259CYWQEYQC8F35).
	TenantId string
}
