package gopartman

import "github.com/jirevwe/gopartman/internal/hooks"

// Hook runs before Retention drops, detaches, or archives a partition.
// The hook is global. It filters by inspecting PartitionRef.Parent. A
// nil hook is equivalent to always returning HookDrop.
type Hook = hooks.Hook

// HookDecision tells Retention what to do with a candidate partition.
type HookDecision = hooks.HookDecision

// PartitionRef identifies a candidate partition passed to a Hook.
type PartitionRef = hooks.PartitionRef

const (
	HookDrop    = hooks.HookDrop
	HookDetach  = hooks.HookDetach
	HookArchive = hooks.HookArchive
	HookSkip    = hooks.HookSkip
)
