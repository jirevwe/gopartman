package go_partman

import "github.com/jirevwe/go_partman/internal/retention"

// SweepReport summarizes one Retention.Sweep call. Aliased from
// internal/retention.
type SweepReport = retention.SweepReport

// SweepOption tunes one call to Retention.Sweep. Aliased from
// internal/retention.
type SweepOption = retention.SweepOption

// WithDryRun makes Retention.Sweep call the Hook and populate the
// SweepReport but emit no DDL and no metadata write.
var WithDryRun = retention.WithDryRun
