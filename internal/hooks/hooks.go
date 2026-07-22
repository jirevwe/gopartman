// Package hooks holds the Hook contract shared between the root
// go_partman facade and internal consumers (retention, maintainer).
// The root package re-exports every type declared here via aliases so
// callers still see partman.Hook, partman.PartitionRef, and the
// HookDrop/HookDetach/HookArchive/HookSkip constants.
package hooks

import (
	"context"

	"github.com/jirevwe/go_partman/internal/naming"
)

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
	Bounds    naming.Bounds
	IsDefault bool
}
