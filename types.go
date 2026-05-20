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
	// Final represents a state that indicates the completion of its parent's
	// process. When the actor's top-level active state has reached "done"
	// (an atomic Final, a compound whose active child is done, or a parallel
	// whose every region is done), the actor stops itself automatically.
	// See [Actor.Stop] for the shutdown contract.
	//
	// Machines that contain no Final state never auto-stop; their actors run
	// until [Actor.Stop] is called explicitly.
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
type Machine[S ~string, E ~string, D Cloner[D]] struct {
	// ID identifies this machine definition.
	ID string
	// Initial is the state to enter when the machine starts.
	Initial S
	// States is a flat map of every state (including nested) for O(1) lookup.
	States map[S]*StateDef[S, E, D]
}

// StateDef defines the properties and behavior of a single state in the machine.
type StateDef[S ~string, E ~string, D Cloner[D]] struct {
	// ID uniquely identifies this state within the machine.
	ID S
	// Type is the kind of state: Atomic, Compound, Parallel, or Final.
	Type StateType
	// Initial is the default child state to enter for Compound states.
	Initial S
	// States maps child state IDs to their definitions.
	States map[S]*StateDef[S, E, D]
	// Transitions maps event IDs to ordered transition definitions.
	// The first transition whose guard passes (or has no guard) fires.
	Transitions map[E][]*TransitionDef[S, E, D]
	// Always holds eventless transitions evaluated on state entry in declaration order.
	Always []*TransitionDef[S, E, D]
	// Delayed holds time-based transitions that fire after their After duration.
	Delayed []*TransitionDef[S, E, D]
	// Entry holds functions called in order when entering this state.
	Entry []func(D) D
	// Exit holds functions called in order when leaving this state.
	Exit []func(D) D
	// Invoke defines an async service started on entry and cancelled on exit.
	Invoke *InvokeDef[S, E, D]
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
	// entryLabel is a human-readable label for Entry actions, used in Mermaid output.
	entryLabel string
	// exitLabel is a human-readable label for Exit actions, used in Mermaid output.
	exitLabel string
}

// TransitionDef defines the rules for moving from one state to another.
type TransitionDef[S ~string, E ~string, D Cloner[D]] struct {
	// Target is the state to transition to.
	Target S
	// Guard is an optional predicate that must return true for the transition to fire.
	Guard func(D) bool
	// Action is a pure function that updates the data during the transition.
	Action func(D) D
	// After is the delay before a timed transition fires.
	After time.Duration

	// guardName is a human-readable label for the guard, used in SCXML export.
	guardName string
	// actionName is a human-readable label for the action, used in SCXML export.
	actionName string
}

// InvokeDef defines an asynchronous service managed during a state's lifecycle.
// The service is started in a goroutine on state entry and cancelled on exit.
type InvokeDef[S ~string, E ~string, D Cloner[D]] struct {
	// Func is the function to run. It receives a context cancelled on state exit,
	// a defensive snapshot of the data taken at state entry, and a thread-safe
	// mutate callback for applying writes to the live data.
	//
	// Parameters:
	// - ctx: cancelled when the state exits or the actor stops. Standard context.Context semantics.
	// - snap: a deep copy of the actor's data taken at the moment of state entry, via D.Clone().
	//   Reads are lock-free and never race because the invoke goroutine owns this value.
	// - mutate: applies updates to the live actor data under the actor's write lock.
	//   The result of the mutation function replaces the live data. mutate is synchronous
	//   and returns after the write commits. It no-ops if the state has exited or the actor stopped.
	//
	// This field was previously named Src.
	Func func(ctx context.Context, snap D, mutate func(func(D) D)) error
	// OnDone is the target state when Func returns nil.
	OnDone S
	// OnError is the target state when Func returns a non-nil error.
	OnError S

	// label is a human-readable label for this invocation, used in Mermaid output.
	label string
}

// Cloner is required of every data type used with a Machine.
// Clone() must return an independent deep copy: mutations to the returned
// value must not be observable through any reference to the original.
// For struct types containing no references (no pointers, slices, maps, channels, or funcs),
// func (c T) Clone() T { return c } is sufficient. For pointer types, ensure
// referenced data (slices, maps, nested structs) is also deep-copied.
type Cloner[T any] interface {
	Clone() T
}

// Snapshot is a serializable point-in-time capture of an [Actor]'s state.
type Snapshot[S ~string, D Cloner[D]] struct {
	// Active lists the currently active state IDs.
	Active []S `json:"active"`
	// History maps compound state IDs to their last active child.
	History map[S]S `json:"history"`
	// Data is the deep-copied data, captured safely using [Cloner.Clone].
	Data D `json:"data"`
	// ActorID is the stable identifier of the actor that produced this snapshot.
	// [Hydrate] restores it so telemetry correlation survives serialization.
	ActorID ActorID `json:"actor_id,omitempty"`
}

