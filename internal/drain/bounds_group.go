package drain

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/jirevwe/go_partman/internal/naming"
	"github.com/jirevwe/go_partman/internal/provisioner"
)

// groupKey identifies one target partition. The zero-value Bounds is
// reserved for anomalies that record NULL control-column tenants; the
// grouping code never emits a zero-value Bounds because rows with a
// NULL control column are filtered out in the read.
type groupKey struct {
	Tenant   string
	TenantOK bool
	Bounds   naming.Bounds
}

// batchRow is one row read from the default partition. TenantOK is
// false when the parent has no tenant column at all.
type batchRow struct {
	CTID     pgtype.TID
	Control  time.Time
	Tenant   string
	TenantOK bool
}

// groupByBounds sorts a batch into per-target buckets keyed by tenant
// and computed bounds. Rows with a control column that fails bounds
// computation are dropped and the error is returned. Callers must have
// filtered NULL control columns out earlier.
func groupByBounds(rows []batchRow, interval time.Duration) (map[groupKey][]pgtype.TID, error) {
	out := make(map[groupKey][]pgtype.TID, len(rows))
	for _, r := range rows {
		b, err := provisioner.BoundsFor(r.Control, interval)
		if err != nil {
			return nil, err
		}
		k := groupKey{
			Tenant:   r.Tenant,
			TenantOK: r.TenantOK,
			Bounds:   b,
		}
		out[k] = append(out[k], r.CTID)
	}
	return out, nil
}
