package retention

// SweepOption tunes one call to Sweep. Options are pure setters that
// mutate the internal sweepOptions struct.
type SweepOption func(*sweepOptions)

type sweepOptions struct {
	dryRun bool
}

// WithDryRun makes Sweep call the Hook and populate the SweepReport
// but emit no DDL and no metadata write. Operators use this to
// preview retention before enabling it.
func WithDryRun(v bool) SweepOption {
	return func(o *sweepOptions) { o.dryRun = v }
}

func evalSweepOptions(opts []SweepOption) sweepOptions {
	var o sweepOptions
	for _, opt := range opts {
		opt(&o)
	}
	return o
}
