package gstate

import (
	"context"
	"fmt"
	"time"
)

// MachineBuilder provides a fluent API for declaring statechart definitions.
type MachineBuilder[S ~string, E ~string, C Cloner[C]] struct {
	machine *Machine[S, E, C]
}

// New initiates the creation of a new statechart machine definition.
func New[S ~string, E ~string, C Cloner[C]](id string) *MachineBuilder[S, E, C] {
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
// It performs a static-analysis validation pass and panics if any invalid state,
// transition target, or invoke target is detected.
func (m *MachineBuilder[S, E, C]) Build() *Machine[S, E, C] {
	// Pre-compute metadata for all states
	for id, state := range m.machine.States {
		if state.parent == "" {
			m.computeMetadata(id, 0, []S{})
		}
	}

	m.validate()

	return m.machine
}

func (m *MachineBuilder[S, E, C]) validate() {
	if m.machine.Initial != "" {
		if _, ok := m.machine.States[m.machine.Initial]; !ok {
			panic(fmt.Errorf("gstate: machine %q has invalid initial state: %q does not exist", m.machine.ID, m.machine.Initial))
		}
	}

	for id, state := range m.machine.States {
		// 1. For compound states, verify initial child is valid if defined
		if state.Type == Compound && state.Initial != "" {
			if _, ok := state.States[state.Initial]; !ok {
				panic(fmt.Errorf("gstate: machine %q compound state %q has invalid initial state: %q is not a direct child state", m.machine.ID, id, state.Initial))
			}
		}

		// 2. Verify all transitions specify a valid, existing state target
		for event, transitions := range state.Transitions {
			for _, t := range transitions {
				if t.Target != "" {
					if _, ok := m.machine.States[t.Target]; !ok {
						panic(fmt.Errorf("gstate: machine %q state %q has invalid transition on event %q: target %q does not exist", m.machine.ID, id, event, t.Target))
					}
				}
			}
		}

		// 3. Verify all always transitions specify a valid, existing state target
		for _, t := range state.Always {
			if t.Target != "" {
				if _, ok := m.machine.States[t.Target]; !ok {
					panic(fmt.Errorf("gstate: machine %q state %q has invalid Always transition: target %q does not exist", m.machine.ID, id, t.Target))
				}
			}
		}

		// 4. Verify all delayed transitions specify a valid, existing state target
		for _, t := range state.Delayed {
			if t.Target != "" {
				if _, ok := m.machine.States[t.Target]; !ok {
					panic(fmt.Errorf("gstate: machine %q state %q has invalid Delayed transition: target %q does not exist", m.machine.ID, id, t.Target))
				}
			}
		}

		// 5. Verify all invoke transitions specify valid, existing state targets
		if state.Invoke != nil {
			if state.Invoke.OnDone != "" {
				if _, ok := m.machine.States[state.Invoke.OnDone]; !ok {
					panic(fmt.Errorf("gstate: machine %q state %q has invalid Invoke OnDone target: %q does not exist", m.machine.ID, id, state.Invoke.OnDone))
				}
			}
			if state.Invoke.OnError != "" {
				if _, ok := m.machine.States[state.Invoke.OnError]; !ok {
					panic(fmt.Errorf("gstate: machine %q state %q has invalid Invoke OnError target: %q does not exist", m.machine.ID, id, state.Invoke.OnError))
				}
			}
		}
	}
}

func (m *MachineBuilder[S, E, C]) computeMetadata(id S, depth int, path []S) {
	state, ok := m.machine.States[id]
	if !ok {
		return
	}

	newPath := make([]S, len(path)+1)
	copy(newPath, path)
	newPath[len(path)] = id

	state.depth = depth
	state.path = newPath

	for childID := range state.States {
		m.computeMetadata(childID, depth+1, newPath)
	}
}

// StateBuilder provides methods for configuring a specific state's properties and children.
type StateBuilder[S ~string, E ~string, C Cloner[C]] struct {
	machine *Machine[S, E, C]
	state   *StateDef[S, E, C]
}

// State defines a nested child state.
func (s *StateBuilder[S, E, C]) State(id S, fn func(*StateBuilder[S, E, C])) {
	child := &StateBuilder[S, E, C]{
		machine: s.machine,
		state: &StateDef[S, E, C]{
			ID:          id,
			parent:      s.state.ID,
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

// EntryLabel sets a human-readable label for the state's Entry actions.
// Used in Mermaid output to identify what runs on entry.
func (s *StateBuilder[S, E, C]) EntryLabel(name string) {
	s.state.entryLabel = name
}

// Exit adds a function to be executed when this state is left.
func (s *StateBuilder[S, E, C]) Exit(fn func(C) C) {
	s.state.Exit = append(s.state.Exit, fn)
}

// ExitLabel sets a human-readable label for the state's Exit actions.
// Used in Mermaid output to identify what runs on exit.
func (s *StateBuilder[S, E, C]) ExitLabel(name string) {
	s.state.exitLabel = name
}

// Invoke configures an asynchronous service to run during the state's lifecycle.
//
// Parameters:
// - fn: service function receiving ctx, entry snapshot, and mutate callback.
//   For details on the parameter contracts, see the documentation for InvokeDef.Func.
// - onDone: state to transition to on success (when fn returns nil).
// - onError: state to transition to on failure (when fn returns a non-nil error).
func (s *StateBuilder[S, E, C]) Invoke(fn func(ctx context.Context, snap C, mutate func(func(C) C)) error, onDone S, onError S) {
	s.state.Invoke = &InvokeDef[S, E, C]{
		Func:    fn,
		OnDone:  onDone,
		OnError: onError,
	}
}

// InvokeLabel sets a human-readable label for the state's invoked service.
// Used in Mermaid output (e.g. as the diamond pseudo-state label).
// No-op if Invoke has not been called yet.
func (s *StateBuilder[S, E, C]) InvokeLabel(name string) {
	if s.state.Invoke != nil {
		s.state.Invoke.label = name
	}
}

// History enables history tracking for this state.
func (s *StateBuilder[S, E, C]) History(t HistoryType) {
	s.state.History = t
}

// On defines a transition triggered by a specific event.
func (s *StateBuilder[S, E, C]) On(event E) *TransitionBuilder[S, E, C] {
	t := &TransitionBuilder[S, E, C]{}
	if _, exists := s.state.Transitions[event]; !exists {
		s.state.eventOrder = append(s.state.eventOrder, event)
	}
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
type TransitionBuilder[S ~string, E ~string, C Cloner[C]] struct {
	def TransitionDef[S, E, C]
}

// Guard adds a conditional check to the transition.
func (t *TransitionBuilder[S, E, C]) Guard(fn func(C) bool) *TransitionBuilder[S, E, C] {
	t.def.Guard = fn
	return t
}

// GuardLabel sets an optional label for the guard condition.
func (t *TransitionBuilder[S, E, C]) GuardLabel(name string) *TransitionBuilder[S, E, C] {
	t.def.guardName = name
	return t
}

// Assign adds a context update action to the transition.
func (t *TransitionBuilder[S, E, C]) Assign(fn func(C) C) *TransitionBuilder[S, E, C] {
	t.def.Action = fn
	return t
}

// ActionLabel sets an optional label for the action.
func (t *TransitionBuilder[S, E, C]) ActionLabel(name string) *TransitionBuilder[S, E, C] {
	t.def.actionName = name
	return t
}

// GoTo sets the target state for the transition.
func (t *TransitionBuilder[S, E, C]) GoTo(target S) {
	t.def.Target = target
}
