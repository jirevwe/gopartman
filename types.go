package go_partman

import (
	"fmt"
	"time"
)

const (
	PartitionMonthInterval = time.Hour * 24 * 30 // 30 days (1 month)
	PartitionWeekInterval  = time.Hour * 24 * 7
	PartitionDayInterval   = time.Hour * 24
	PartitionHourInterval  = time.Hour
)

const (
	DateNoHyphens = "20060102"
)

type Bounds struct {
	From, To time.Time
}

type D struct {
	Key   string
	Value string
}

// Tenant represents a tenant configuration for a specific parent table
type Tenant struct {
	// ParentName references the parent table this tenant belongs to
	ParentName string

	// ParentSchema references the parent table schema
	ParentSchema string

	// TenantId Tenant ID column value (e.g., 01J2V010NV1259CYWQEYQC8F35)
	TenantId string
}

// Update partition name and SQL formatting to use UTC
func generatePartitionName(tc Tenant, b Bounds) string {
	datePart := b.From.UTC().Format(DateNoHyphens)

	if len(tc.TenantId) > 0 {
		return fmt.Sprintf("%s_%s_%s", tc.ParentName, tc.TenantId, datePart)
	}
	return fmt.Sprintf("%s_%s", tc.ParentSchema, datePart)
}

type tableName string

func buildTableName(schema, table, tenantId string, now time.Time, interval time.Duration) tableName {
	if schema == "" {
		schema = "public"
	}

	var tn string

	if tenantId != "" && len(tenantId) > 0 {
		tn = fmt.Sprintf("%s.%s_%s", schema, table, tenantId)
	}
	tn = fmt.Sprintf("%s.%s", schema, table)

	return tableName(tn)
}
