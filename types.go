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

// Machine is the static, immutable definition of a statechart.
// It serves as a blueprint for creating [Actor] instances.
type Machine[S ~string, E ~string, C any] struct {
	// ID identifies this machine definition.
	ID string
	// Initial is the state to enter when the machine starts.
	Initial S
	// States is a flat map of every state (including nested) for O(1) lookup.
	States map[S]*StateDef[S, E, C]
}

// StateDef defines the properties and behavior of a single state in the machine.
type StateDef[S ~string, E ~string, C any] struct {
	// ID uniquely identifies this state within the machine.
	ID S
	// Type is the kind of state: Atomic, Compound, Parallel, or Final.
	Type StateType
	// Initial is the default child state to enter for Compound states.
	Initial S
	// States maps child state IDs to their definitions.
	States map[S]*StateDef[S, E, C]
	// Transitions maps event IDs to ordered transition definitions.
	// The first transition whose guard passes (or has no guard) fires.
	Transitions map[E][]*TransitionDef[S, E, C]
	// Always holds eventless transitions evaluated on state entry in declaration order.
	Always []*TransitionDef[S, E, C]
	// Delayed holds time-based transitions that fire after their After duration.
	Delayed []*TransitionDef[S, E, C]
	// Entry holds functions called in order when entering this state.
	Entry []func(C) C
	// Exit holds functions called in order when leaving this state.
	Exit []func(C) C
	// Invoke defines an async service started on entry and cancelled on exit.
	Invoke *InvokeDef[S, E, C]
	// History controls history tracking: None, Shallow, or Deep.
	History HistoryType

	// parent is the ID of the enclosing state, or zero for top-level states.
	parent S
	// depth is the distance from the root, used for LCA calculation.
	depth int
	// path is the ancestor chain from root to this state, inclusive.
	path []S
	// eventOrder tracks declaration order of transition event keys.
	eventOrder []E
}

// TransitionDef defines the rules for moving from one state to another.
type TransitionDef[S ~string, E ~string, C any] struct {
	// Target is the state to transition to.
	Target S
	// Guard is an optional predicate that must return true for the transition to fire.
	Guard func(C) bool
	// Action is a pure function that updates the context during the transition.
	Action func(C) C
	// After is the delay before a timed transition fires.
	After time.Duration

	// guardName is a human-readable label for the guard, used in SCXML export.
	guardName string
	// actionName is a human-readable label for the action, used in SCXML export.
	actionName string
}

// InvokeDef defines an asynchronous service managed during a state's lifecycle.
// The service is started in a goroutine on state entry and cancelled on exit.
type InvokeDef[S ~string, E ~string, C any] struct {
	// Src is the function to run. It receives a context that is cancelled on state exit.
	Src func(context.Context, C) error
	// OnDone is the target state when Src returns nil.
	OnDone S
	// OnError is the target state when Src returns a non-nil error.
	OnError S
}

// Cloner is an optional interface that Context types can implement to provide
// safe deep-copying during snapshots.
type Cloner[C any] interface {
	Clone() C
}

// Snapshot is a serializable point-in-time capture of an [Actor]'s state.
type Snapshot[S ~string, C any] struct {
	// Active lists the currently active state IDs.
	Active []S `json:"active"`
	// History maps compound state IDs to their last active child.
	History map[S]S `json:"history"`
	// Context is the current data. If C contains reference types,
	// implement [Cloner] for safe deep-copying.
	Context C `json:"context"`
	// ActorID is the stable identifier of the actor that produced this snapshot.
	// [Hydrate] restores it so telemetry correlation survives serialization.
	ActorID ActorID `json:"actor_id,omitempty"`
}
