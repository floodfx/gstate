package gstate

import (
	"context"
	"time"
)

// StateType represents the type of a state in the statechart.
type StateType int

const (
	// Atomic represents a leaf state with no children.
	Atomic StateType = iota
	// Compound represents a state that contains one or more child states.
	// It has exactly one active child at any given time.
	Compound
	// Parallel represents a state where all of its child regions are active simultaneously.
	Parallel
	// Final represents a state that indicates the completion of its parent's process.
	Final
)

// HistoryType represents the type of history tracking for a compound state.
// When history is enabled, entering a compound state will restore the last active child
// instead of defaulting to the initial state.
type HistoryType int

const (
	// None means no history is tracked for the state.
	None HistoryType = iota
	// Shallow remembers the direct child state that was active.
	Shallow
	// Deep remembers all active descendants in the state hierarchy.
	Deep
)

// Machine is the static definition of a statechart.
// It acts as a blueprint for creating Actor instances.
// S = StateID type, E = EventID type, C = Context type.
type Machine[S ~string, E ~string, C any] struct {
	// ID is a unique identifier for the machine.
	ID string
	// Initial is the ID of the state to enter when the machine starts.
	Initial S
	// States is a flat map of all states in the machine for efficient lookup.
	States map[S]*StateDef[S, E, C]
}

// StateDef defines the properties and behavior of a single state in the machine.
type StateDef[S ~string, E ~string, C any] struct {
	// ID is the unique identifier for this state.
	ID S
	// Type determines if the state is Atomic, Compound, Parallel, or Final.
	Type StateType
	// Initial is the child state to enter by default (only for Compound states).
	Initial S
	// Parent is the ID of the parent state, if any.
	Parent S
	// Depth is the pre-computed distance from the root state.
	Depth int
	// Path is the pre-computed slice of ancestor IDs from root to this state.
	Path []S
	// States is the map of direct child states.
	States map[S]*StateDef[S, E, C]
	// Transitions is a map of events to their corresponding transition definitions.
	Transitions map[E][]*TransitionDef[S, E, C]
	// Always defines transient transitions that fire immediately when guards are met.
	Always []*TransitionDef[S, E, C]
	// Delayed defines transitions that fire after a specific duration of time.
	Delayed []*TransitionDef[S, E, C]
	// Entry is a list of functions called when entering this state.
	Entry []func(C) C
	// Exit is a list of functions called when leaving this state.
	Exit []func(C) C
	// Invoke defines an asynchronous service to be managed during the state's lifecycle.
	Invoke *InvokeDef[S, E, C]
	// History specifies the type of history tracking for this state.
	History HistoryType
}

// TransitionDef defines the rules for moving from one state to another.
type TransitionDef[S ~string, E ~string, C any] struct {
	// Target is the ID of the state to transition to.
	Target S
	// Guard is an optional condition that must be true for the transition to fire.
	Guard func(C) bool
	// Action (Assign) is a pure function that updates the context during the transition.
	Action func(C) C
	// After is the duration to wait before a delayed transition fires.
	After time.Duration
}

// InvokeDef defines an asynchronous service launched on state entry.
type InvokeDef[S ~string, E ~string, C any] struct {
	// Src is the function to execute in a separate goroutine.
	Src func(context.Context, C) error
	// OnDone is the state to transition to if Src returns nil.
	OnDone S
	// OnError is the state to transition to if Src returns an error.
	OnError S
}

// Cloner is an optional interface that Context types can implement to provide
// safe deep-copying during snapshots.
type Cloner[C any] interface {
	Clone() C
}

// Snapshot represents a serializable point-in-time state of an Actor.
type Snapshot[S ~string, C any] struct {
	// Active is the list of currently active state IDs.
	Active []S `json:"active"`
	// History is a map of parent states to their last active child states.
	History map[S]S `json:"history"`
	// Context is the current state of the machine's data.
	// Note: If C contains reference types (pointers, slices, maps), ensure they are
	// handled safely during serialization or implement the Cloner interface.
	Context C `json:"context"`
}
