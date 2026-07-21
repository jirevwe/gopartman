package go_partman

import "context"

// Hook runs before Retention drops, detaches, or archives a partition.
// The hook is global. It filters by inspecting PartitionRef.Parent. A
// nil hook is equivalent to always returning HookDrop.
type Hook func(ctx context.Context, ref PartitionRef) HookDecision

// HookDecision tells Retention what to do with a candidate partition.
type HookDecision int

const (
	HookDrop HookDecision = iota
	HookDetach
	HookArchive
	HookSkip
)

// PartitionRef identifies a candidate partition passed to a Hook.
type PartitionRef struct {
	Schema    string
	Parent    string
	TenantId  string
	Bounds    Bounds
	IsDefault bool
}
