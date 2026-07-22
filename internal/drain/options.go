package drain

// Options bundles the tuning knobs for one PartitionData call. The
// facade in the root package translates its public DrainOption slice
// into this struct at the package boundary.
type Options struct {
	// BatchSize is the maximum rows read per batch. Zero means "use the
	// default".
	BatchSize int
	// MaxBatches limits the number of batches per call. Zero means
	// "unlimited".
	MaxBatches int
	// Tenant, when non-nil, filters reads to one tenant only. Nil means
	// "all tenants".
	Tenant *string
}

const defaultBatchSize = 1000

func (o Options) resolved() Options {
	r := o
	if r.BatchSize <= 0 {
		r.BatchSize = defaultBatchSize
	}
	if r.MaxBatches < 0 {
		r.MaxBatches = 0
	}
	return r
}
