package gstate

import (
	"context"
	"time"
)

// MachineBuilder provides a fluent API for declaring statechart definitions.
type MachineBuilder[S ~string, E ~string, C any] struct {
	machine *Machine[S, E, C]
}

// New initiates the creation of a new statechart machine definition.
func New[S ~string, E ~string, C any](id string) *MachineBuilder[S, E, C] {
	return &MachineBuilder[S, E, C]{
		machine: &Machine[S, E, C]{
			ID:     id,
			States: make(map[S]*StateDef[S, E, C]),
		},
	}
}

// Initial sets the starting state for the machine.
func (m *MachineBuilder[S, E, C]) Initial(id S) *MachineBuilder[S, E, C] {
	m.machine.Initial = id
	return m
}

// State defines a top-level state in the machine.
// The provided function is used to configure the state's behavior via a StateBuilder.
func (m *MachineBuilder[S, E, C]) State(id S, fn func(*StateBuilder[S, E, C])) *MachineBuilder[S, E, C] {
	s := &StateBuilder[S, E, C]{
		machine: m.machine,
		state: &StateDef[S, E, C]{
			ID:          id,
			States:      make(map[S]*StateDef[S, E, C]),
			Transitions: make(map[E][]*TransitionDef[S, E, C]),
		},
	}
	fn(s)
	m.machine.States[id] = s.state
	return m
}

// Build finalizes the machine definition and returns an immutable Machine instance.
func (m *MachineBuilder[S, E, C]) Build() *Machine[S, E, C] {
	// Pre-compute metadata for all states
	for id, state := range m.machine.States {
		if state.Parent == "" {
			m.computeMetadata(id, 0, []S{})
		}
	}
	return m.machine
}

func (m *MachineBuilder[S, E, C]) computeMetadata(id S, depth int, path []S) {
	state, ok := m.machine.States[id]
	if !ok {
		return
	}

	newPath := make([]S, len(path)+1)
	copy(newPath, path)
	newPath[len(path)] = id

	state.Depth = depth
	state.Path = newPath

	for childID := range state.States {
		m.computeMetadata(childID, depth+1, newPath)
	}
}

// StateBuilder provides methods for configuring a specific state's properties and children.
type StateBuilder[S ~string, E ~string, C any] struct {
	machine *Machine[S, E, C]
	state   *StateDef[S, E, C]
}

// State defines a nested child state.
func (s *StateBuilder[S, E, C]) State(id S, fn func(*StateBuilder[S, E, C])) {
	child := &StateBuilder[S, E, C]{
		machine: s.machine,
		state: &StateDef[S, E, C]{
			ID:          id,
			Parent:      s.state.ID,
			States:      make(map[S]*StateDef[S, E, C]),
			Transitions: make(map[E][]*TransitionDef[S, E, C]),
		},
	}
	fn(child)
	s.state.States[id] = child.state
	s.machine.States[id] = child.state
	// If a state has children, default its type to Compound unless explicitly set.
	if s.state.Type == Atomic {
		s.state.Type = Compound
	}
}

// Initial sets the default child state to enter for this compound state.
func (s *StateBuilder[S, E, C]) Initial(id S) {
	s.state.Initial = id
}

// Type explicitly sets the StateType (e.g., Parallel or Final).
func (s *StateBuilder[S, E, C]) Type(t StateType) {
	s.state.Type = t
}

// Entry adds a function to be executed when this state is entered.
func (s *StateBuilder[S, E, C]) Entry(fn func(C) C) {
	s.state.Entry = append(s.state.Entry, fn)
}

// Exit adds a function to be executed when this state is left.
func (s *StateBuilder[S, E, C]) Exit(fn func(C) C) {
	s.state.Exit = append(s.state.Exit, fn)
}

// Invoke configures an asynchronous service to run during the state's lifecycle.
// onDone: state to transition to on success.
// onError: state to transition to on failure.
func (s *StateBuilder[S, E, C]) Invoke(fn func(context.Context, C) error, onDone S, onError S) {
	s.state.Invoke = &InvokeDef[S, E, C]{
		Src:     fn,
		OnDone:  onDone,
		OnError: onError,
	}
}

// History enables history tracking for this state.
func (s *StateBuilder[S, E, C]) History(t HistoryType) {
	s.state.History = t
}

// On defines a transition triggered by a specific event.
func (s *StateBuilder[S, E, C]) On(event E) *TransitionBuilder[S, E, C] {
	t := &TransitionBuilder[S, E, C]{}
	s.state.Transitions[event] = append(s.state.Transitions[event], &t.def)
	return t
}

// Always defines a transient transition that fires immediately if its guard passes.
func (s *StateBuilder[S, E, C]) Always() *TransitionBuilder[S, E, C] {
	t := &TransitionBuilder[S, E, C]{}
	s.state.Always = append(s.state.Always, &t.def)
	return t
}

// After defines a transition that fires automatically after a duration.
func (s *StateBuilder[S, E, C]) After(d time.Duration) *TransitionBuilder[S, E, C] {
	t := &TransitionBuilder[S, E, C]{def: TransitionDef[S, E, C]{After: d}}
	s.state.Delayed = append(s.state.Delayed, &t.def)
	return t
}

// TransitionBuilder provides a fluent API for configuring a state transition.
type TransitionBuilder[S ~string, E ~string, C any] struct {
	def TransitionDef[S, E, C]
}

// Guard adds a conditional check to the transition.
func (t *TransitionBuilder[S, E, C]) Guard(fn func(C) bool) *TransitionBuilder[S, E, C] {
	t.def.Guard = fn
	return t
}

// Assign adds a context update action to the transition.
func (t *TransitionBuilder[S, E, C]) Assign(fn func(C) C) *TransitionBuilder[S, E, C] {
	t.def.Action = fn
	return t
}

// GoTo sets the target state for the transition.
func (t *TransitionBuilder[S, E, C]) GoTo(target S) {
	t.def.Target = target
}
