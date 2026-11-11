package gopartman

// PartitionDDLMode selects how Provisioner and Retention issue DDL.
// Classic is the default and matches ADR-0004 and ADR-0006.
type PartitionDDLMode int

const (
	// PartitionDDLModeClassic creates children with CREATE TABLE …
	// PARTITION OF and drops attached children with DROP TABLE.
	PartitionDDLModeClassic PartitionDDLMode = iota

	// PartitionDDLModeOnline prepares standalone children then ATTACH,
	// and drops via DETACH PARTITION CONCURRENTLY then DROP. See
	// ADR-0013. Requires PostgreSQL 14+.
	PartitionDDLModeOnline
)

func (m PartitionDDLMode) String() string {
	switch m {
	case PartitionDDLModeOnline:
		return "online"
	default:
		return "classic"
	}
}
