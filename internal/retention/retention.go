// Package retention drops, detaches, or archives expired partitions.
// ADR-0006 fills in the interface; this file declares the seam.
package retention

// Retention is the interface for the retention sweep. It is
// intentionally empty at the ADR-0001 stage.
type Retention any

// SweepReport summarizes one Retention.Sweep call.
type SweepReport struct {
	Dropped   int
	Detached  int
	Archived  int
	Skipped   int
	Anomalies []string
}
