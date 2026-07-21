package go_partman

// DrainOption tunes the PartitionData drain operation. Concrete
// constructors ship with ADR-0009.
type DrainOption func(*drainOptions)

type drainOptions struct {
	batchSize int //nolint:unused // populated by DrainOption constructors in ADR-0009
}

// DrainReport summarizes one PartitionData drain call. It reports how
// many batches ran, how many rows moved out of the default partition,
// and any anomalies detected.
type DrainReport struct {
	Batches   int
	RowsMoved int
	Anomalies []string
}
