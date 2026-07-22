package gopartman

// DrainOption tunes one PartitionData call. Concrete constructors are
// WithBatchSize, WithMaxBatches, and WithTenant. See ADR-0009.
type DrainOption func(*drainOptions)

type drainOptions struct {
	batchSize  int
	maxBatches int
	tenant     *string
}

// WithBatchSize sets the maximum rows read per batch. Default is 1000.
// A value of zero or below is ignored.
func WithBatchSize(n int) DrainOption {
	return func(o *drainOptions) {
		if n > 0 {
			o.batchSize = n
		}
	}
}

// WithMaxBatches limits the number of batches per call. The default,
// zero, means "no limit — run until the default partition is drained".
// A negative value is ignored.
func WithMaxBatches(n int) DrainOption {
	return func(o *drainOptions) {
		if n >= 0 {
			o.maxBatches = n
		}
	}
}

// WithTenant scopes the drain to one tenant. When set, only rows whose
// tenant column matches the value move; other tenants stay in the
// default partition. Empty string clears the filter.
func WithTenant(t string) DrainOption {
	return func(o *drainOptions) { o.tenant = &t }
}

func evalDrainOptions(opts []DrainOption) drainOptions {
	var o drainOptions
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// DrainReport summarizes one PartitionData call. RowsMoved counts rows
// that moved from the default partition into a target. BatchesRun
// counts the batches the drain executed. Anomalies lists the target
// partitions that were missing and the row counts left in the default.
type DrainReport struct {
	RowsMoved  int
	BatchesRun int
	Anomalies  []DrainAnomaly
}

// DrainAnomaly names one condition the drain could not fix. A zero-value
// MissingPartitionBounds signals "control column was NULL" for the
// tenant; otherwise it names the target bounds that had no partition.
type DrainAnomaly struct {
	MissingPartitionBounds Bounds
	TenantId               string
	RowCount               int
}
