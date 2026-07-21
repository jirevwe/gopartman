package go_partman

// Meter is the observability sink for go_partman. It is empty in v1;
// ADR-0010 will add methods. NoopMeter is the default.
type Meter any

// NoopMeter is a Meter that records nothing. It is the default value
// used when WithMeter is not supplied to New.
type NoopMeter struct{}
