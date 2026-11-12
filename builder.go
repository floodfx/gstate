package gstate

import (
	"context"
	"fmt"
	"time"
)

// validationMessage preserves the string panic values historically used for
// structural validation while also exposing them through the error API.
type validationMessage string

func (message validationMessage) Error() string { return string(message) }

// MachineBuilder provides a fluent API for declaring statechart definitions.
type MachineBuilder[S ~string, E ~string, D Cloner[D]] struct {
	machine *Machine[S, E, D]
}

// New initiates the creation of a new statechart machine definition.
func New[S ~string, E ~string, D Cloner[D]](id string) *MachineBuilder[S, E, D] {
	return &MachineBuilder[S, E, D]{
		machine: &Machine[S, E, D]{
			ID:     id,
			States: make(map[S]*StateDef[S, E, D]),
		},
	}
}

// Initial sets the starting state for the machine. Repeated assignments are
// recorded as validation errors and retain the first value.
func (m *MachineBuilder[S, E, D]) Initial(id S) *MachineBuilder[S, E, D] {
	if m.machine.initialSet {
		m.machine.buildErrors = append(m.machine.buildErrors, fmt.Errorf("gstate: machine initial already set to %q; cannot assign %q", m.machine.Initial, id))
		return m
	}
	m.machine.Initial = id
	m.machine.initialSet = true
	return m
}

// ReplaceInitial replaces the machine's initial state only when it matches expected.
func (m *MachineBuilder[S, E, D]) ReplaceInitial(expected, next S) *MachineBuilder[S, E, D] {
	if !m.machine.initialSet || m.machine.Initial != expected {
		m.machine.buildErrors = append(m.machine.buildErrors, fmt.Errorf("gstate: machine initial replacement expected %q, found %q", expected, m.machine.Initial))
		return m
	}
	m.machine.Initial = next
	return m
}

// State defines a top-level state in the machine.
// The provided function is used to configure the state's behavior via a StateBuilder.
func (m *MachineBuilder[S, E, D]) State(id S, fn func(*StateBuilder[S, E, D])) *MachineBuilder[S, E, D] {
	s := &StateBuilder[S, E, D]{
		machine: m.machine,
		state: &StateDef[S, E, D]{
			ID:          id,
			States:      make(map[S]*StateDef[S, E, D]),
			Transitions: make(map[E][]*TransitionDef[S, E, D]),
		},
	}
	m.reserveState(id, s.state)
	fn(s)
	return m
}

// Extend re-opens an existing top-level state in the machine for additional
// configuration (such as adding transitions, actions, or child states from a
// modular plugin). It panics if id is empty, does not exist, or is a nested child state.
func (m *MachineBuilder[S, E, D]) Extend(id S, fn func(*StateBuilder[S, E, D])) *MachineBuilder[S, E, D] {
	if id == "" {
		panic("gstate: state ID must not be empty")
	}
	existing, ok := m.machine.States[id]
	if !ok || existing == nil {
		panic(fmt.Errorf("gstate: cannot extend unknown state %q", id))
	}
	if existing.parent != "" {
		panic(fmt.Errorf("gstate: state %q is nested under %q; use parent StateBuilder.Extend", id, existing.parent))
	}
	if fn != nil {
		fn(&StateBuilder[S, E, D]{
			machine:      m.machine,
			state:        existing,
			explicitType: existing.explicitType,
		})
	}
	return m
}

// Use applies one or more reusable builder functions (plugins) to the MachineBuilder in order.
func (m *MachineBuilder[S, E, D]) Use(plugins ...func(*MachineBuilder[S, E, D])) *MachineBuilder[S, E, D] {
	for _, p := range plugins {
		if p != nil {
			p(m)
		}
	}
	return m
}

// Build validates and finalizes the machine. Treat it and all retained builder
// references as read-only afterward; see Machine for the ownership contract.
// It performs a static-analysis validation pass and panics if any invalid state,
// transition target, or invoke target is detected.
func (m *MachineBuilder[S, E, D]) Build() *Machine[S, E, D] {
	machine, err := m.BuildWithError()
	if err != nil {
		if message, ok := err.(validationMessage); ok {
			panic(string(message))
		}
		panic(err)
	}
	return machine
}

// BuildWithError validates and finalizes like Build, returning nil and an error
// for an invalid definition. It does not run actions, guards, or invokes.
// State declarations still panic immediately for empty or duplicate IDs, before
// this method can be called. Panics in user-provided State callbacks propagate.
func (m *MachineBuilder[S, E, D]) BuildWithError() (*Machine[S, E, D], error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return m.machine, nil
}

// Validate checks the definition and computes hierarchy metadata. It returns
// the first validation error found, or nil. Error ordering is unspecified when
// multiple defects exist. It runs no user callbacks. Like other builder methods,
// it must not run concurrently with actor use of the machine.
func (m *MachineBuilder[S, E, D]) Validate() error {
	if len(m.machine.buildErrors) > 0 {
		return m.machine.buildErrors[0]
	}
	if err := m.validate(); err != nil {
		return err
	}
	for id, state := range m.machine.States {
		if state.parent == "" {
			m.computeMetadata(id, 0, []S{})
		}
	}
	if err := m.validateCompletion(); err != nil {
		return err
	}
	return m.validateRegions()
}

func (m *MachineBuilder[S, E, D]) reserveState(id S, state *StateDef[S, E, D]) {
	if id == "" {
		panic("gstate: state ID must not be empty")
	}
	if _, exists := m.machine.States[id]; exists {
		panic(fmt.Errorf("gstate: duplicate state ID %q", id))
	}
	m.machine.States[id] = state
}

// Validate the tree before any recursive metadata traversal.
func (m *MachineBuilder[S, E, D]) validateStructure() error {
	initial := m.machine.States[m.machine.Initial]
	if m.machine.Initial == "" {
		return validationMessage("gstate: machine initial state is required")
	}
	if initial != nil && initial.parent != "" {
		return validationMessage("gstate: machine initial must be top-level")
	}
	visited := make(map[S]bool)
	var visit func(S) error
	visit = func(id S) error {
		sd := m.machine.States[id]
		if sd == nil || visited[id] {
			return validationMessage("gstate: invalid hierarchy or cycle")
		}
		visited[id] = true
		for child, def := range sd.States {
			if def == nil || def.parent != id || m.machine.States[child] != def {
				return validationMessage("gstate: invalid child hierarchy")
			}
			if err := visit(child); err != nil {
				return err
			}
		}
		return nil
	}
	for id, sd := range m.machine.States {
		if sd == nil || sd.ID != id || id == "" {
			return validationMessage("gstate: invalid state definition")
		}
		if len(sd.Entry) != len(sd.entryLabels) || len(sd.Exit) != len(sd.exitLabels) {
			return fmt.Errorf("gstate: state %q action label metadata is not aligned", id)
		}
		if len(sd.OnDone) > 0 && sd.Type != Compound && sd.Type != Parallel {
			return fmt.Errorf("gstate: state %q OnDone requires a compound or parallel state", id)
		}
		switch sd.Type {
		case Atomic, Final:
			if len(sd.States) > 0 || sd.Initial != "" {
				return validationMessage("gstate: atomic/final state cannot have children or initial")
			}
			if sd.Type == Final && (len(sd.Transitions) > 0 || len(sd.Always) > 0 || len(sd.Delayed) > 0 || sd.Invoke != nil) {
				return validationMessage("gstate: final state cannot have outgoing transitions or services")
			}
		case Compound, Parallel:
			if len(sd.States) == 0 {
				return validationMessage("gstate: compound/parallel state requires children")
			}
			if sd.Type == Compound && sd.Initial == "" {
				return validationMessage("gstate: compound initial state is required")
			}
			if sd.Type == Parallel && sd.Initial != "" {
				return validationMessage("gstate: parallel state cannot specify an initial child")
			}
		default:
			return validationMessage("gstate: invalid state kind")
		}
		if sd.History < None || sd.History > Deep || (sd.History != None && sd.Type != Compound && sd.Type != Parallel) {
			return validationMessage("gstate: invalid history kind")
		}
	}
	for id, sd := range m.machine.States {
		if sd.parent == "" {
			if err := visit(id); err != nil {
				return err
			}
		}
	}
	if len(visited) != len(m.machine.States) {
		return validationMessage("gstate: disconnected or cyclic hierarchy")
	}
	return nil
}

func (m *MachineBuilder[S, E, D]) validateRegions() error {
	for id, sd := range m.machine.States {
		targets := []S{}
		for _, ts := range sd.Transitions {
			for _, t := range ts {
				targets = append(targets, t.Target)
			}
		}
		for _, t := range sd.OnDone {
			targets = append(targets, t.Target)
		}
		for _, t := range sd.Always {
			targets = append(targets, t.Target)
		}
		for _, t := range sd.Delayed {
			targets = append(targets, t.Target)
		}
		if sd.Invoke != nil {
			targets = append(targets, sd.Invoke.OnDone, sd.Invoke.OnError)
		}
		for _, target := range targets {
			if target == "" {
				continue
			}
			tp := m.machine.States[target].path
			for i, ancestor := range sd.path {
				if i+1 < len(sd.path) && i+1 < len(tp) && tp[i] == ancestor && m.machine.States[ancestor].Type == Parallel && sd.path[i+1] != tp[i+1] {
					return fmt.Errorf("gstate: cross-region transition from %q to %q is unsupported", id, target)
				}
			}
		}
	}
	return nil
}

// validateCompletion checks finite-workflow structure, not reachability or guards.
func (m *MachineBuilder[S, E, D]) validateCompletion() error {
	var canComplete func(*StateDef[S, E, D]) bool
	canComplete = func(sd *StateDef[S, E, D]) bool {
		switch sd.Type {
		case Final:
			return true
		case Compound:
			for _, child := range sd.States {
				if child.Type == Final {
					return true
				}
			}
		case Parallel:
			for _, child := range sd.States {
				if !canComplete(child) {
					return false
				}
			}
			return len(sd.States) > 0
		}
		return false
	}
	for id, sd := range m.machine.States {
		if len(sd.OnDone) == 0 {
			continue
		}
		if !canComplete(sd) {
			return fmt.Errorf("gstate: state %q OnDone cannot complete: requires direct final children or completable parallel regions", id)
		}
		for i, t := range sd.OnDone {
			if t == nil {
				return fmt.Errorf("gstate: state %q has nil OnDone candidate", id)
			}
			if t.Target == "" {
				return fmt.Errorf("gstate: state %q OnDone requires a target outside its subtree", id)
			}
			target, exists := m.machine.States[t.Target]
			if !exists {
				return fmt.Errorf("gstate: state %q OnDone target %q does not exist", id, t.Target)
			}
			for _, ancestor := range target.path {
				if ancestor == id {
					return fmt.Errorf("gstate: state %q OnDone target %q is inside its completed source subtree", id, t.Target)
				}
			}
			if i < len(sd.OnDone)-1 && t.Guard == nil {
				return fmt.Errorf("gstate: state %q OnDone candidate follows unconditional fallback", id)
			}
		}
		if sd.OnDone[len(sd.OnDone)-1].Guard != nil {
			return fmt.Errorf("gstate: state %q OnDone requires a final unconditional fallback", id)
		}
	}
	return nil
}

func (m *MachineBuilder[S, E, D]) validate() error {
	if err := m.validateStructure(); err != nil {
		return err
	}
	if m.machine.Initial != "" {
		if _, ok := m.machine.States[m.machine.Initial]; !ok {
			return fmt.Errorf("gstate: machine %q has invalid initial state: %q does not exist", m.machine.ID, m.machine.Initial)
		}
	}

	for id, state := range m.machine.States {
		// 1. For compound states, verify initial child is valid if defined
		if state.Type == Compound && state.Initial != "" {
			if _, ok := state.States[state.Initial]; !ok {
				return fmt.Errorf("gstate: machine %q compound state %q has invalid initial state: %q is not a direct child state", m.machine.ID, id, state.Initial)
			}
		}

		// 2. Verify all transitions specify a valid, existing state target
		for event, transitions := range state.Transitions {
			for _, t := range transitions {
				if t.Target != "" {
					if _, ok := m.machine.States[t.Target]; !ok {
						return fmt.Errorf("gstate: machine %q state %q has invalid transition on event %q: target %q does not exist", m.machine.ID, id, event, t.Target)
					}
				}
			}
		}

		// 3. Verify all always transitions specify a valid, existing state target
		for _, t := range state.Always {
			if t.Target != "" {
				if _, ok := m.machine.States[t.Target]; !ok {
					return fmt.Errorf("gstate: machine %q state %q has invalid Always transition: target %q does not exist", m.machine.ID, id, t.Target)
				}
			}
		}

		// 4. Verify all delayed transitions specify a valid, existing state target
		for _, t := range state.Delayed {
			if t.Target != "" {
				if _, ok := m.machine.States[t.Target]; !ok {
					return fmt.Errorf("gstate: machine %q state %q has invalid Delayed transition: target %q does not exist", m.machine.ID, id, t.Target)
				}
			}
		}

		// 5. Verify all invoke transitions specify valid, existing state targets
		if state.Invoke != nil {
			if state.Invoke.OnDone != "" {
				if _, ok := m.machine.States[state.Invoke.OnDone]; !ok {
					return fmt.Errorf("gstate: machine %q state %q has invalid Invoke OnDone target: %q does not exist", m.machine.ID, id, state.Invoke.OnDone)
				}
			}
			if state.Invoke.OnError != "" {
				if _, ok := m.machine.States[state.Invoke.OnError]; !ok {
					return fmt.Errorf("gstate: machine %q state %q has invalid Invoke OnError target: %q does not exist", m.machine.ID, id, state.Invoke.OnError)
				}
			}
		}
	}
	return nil
}

func (m *MachineBuilder[S, E, D]) computeMetadata(id S, depth int, path []S) {
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
type StateBuilder[S ~string, E ~string, D Cloner[D]] struct {
	machine      *Machine[S, E, D]
	state        *StateDef[S, E, D]
	explicitType bool
}

// State defines a nested child state.
func (s *StateBuilder[S, E, D]) State(id S, fn func(*StateBuilder[S, E, D])) {
	child := &StateBuilder[S, E, D]{
		machine: s.machine,
		state: &StateDef[S, E, D]{
			ID:          id,
			parent:      s.state.ID,
			States:      make(map[S]*StateDef[S, E, D]),
			Transitions: make(map[E][]*TransitionDef[S, E, D]),
		},
	}
	mb := MachineBuilder[S, E, D]{machine: s.machine}
	mb.reserveState(id, child.state)
	fn(child)
	s.state.States[id] = child.state
	// If a state has children, default its type to Compound unless explicitly set.
	if s.state.Type == Atomic && !s.explicitType && !s.state.explicitType {
		s.state.Type = Compound
	}
}

// Extend re-opens an existing direct child state of this state for additional
// configuration. It panics if id is empty or is not a direct child of this state.
func (s *StateBuilder[S, E, D]) Extend(id S, fn func(*StateBuilder[S, E, D])) {
	if id == "" {
		panic("gstate: state ID must not be empty")
	}
	existing, ok := s.state.States[id]
	if !ok || existing == nil {
		panic(fmt.Errorf("gstate: state %q is not a direct child of %q", id, s.state.ID))
	}
	if fn != nil {
		fn(&StateBuilder[S, E, D]{
			machine:      s.machine,
			state:        existing,
			explicitType: existing.explicitType,
		})
	}
}

// Use applies one or more reusable builder functions (plugins) to this StateBuilder in order.
func (s *StateBuilder[S, E, D]) Use(plugins ...func(*StateBuilder[S, E, D])) {
	for _, p := range plugins {
		if p != nil {
			p(s)
		}
	}
}

// Initial sets the default child state to enter for this compound state.
// A second assignment records a validation error; use ReplaceInitial instead.
func (s *StateBuilder[S, E, D]) Initial(id S) {
	if s.state.initialSet {
		s.recordError("initial already set to %q; cannot assign %q (use ReplaceInitial)", s.state.Initial, id)
		return
	}
	s.state.Initial = id
	s.state.initialSet = true
}

// ReplaceInitial replaces an initial child only when it matches expected.
func (s *StateBuilder[S, E, D]) ReplaceInitial(expected, next S) {
	if !s.state.initialSet || s.state.Initial != expected {
		s.recordError("initial replacement expected %q, found %q", expected, s.state.Initial)
		return
	}
	s.state.Initial = next
}

// Type explicitly sets the StateType (e.g., Parallel or Final). Repeating the
// same type is allowed; conflicting explicit types are validation errors.
func (s *StateBuilder[S, E, D]) Type(t StateType) {
	if s.state.explicitType && s.state.Type != t {
		s.recordError("type already explicitly set to %v; cannot assign %v", s.state.Type, t)
		return
	}
	s.state.Type = t
	s.explicitType = true
	s.state.explicitType = true
}

// Entry adds a function to be executed when this state is entered.
func (s *StateBuilder[S, E, D]) Entry(fn func(D) D) {
	s.state.Entry = append(s.state.Entry, fn)
	s.state.entryLabels = append(s.state.entryLabels, "")
}

// EntryLabel names the most recently appended entry action. Empty or repeated labels are invalid.
func (s *StateBuilder[S, E, D]) EntryLabel(name string) {
	s.labelAction("entry", s.state.entryLabels, name)
}

// Exit adds a function to be executed when this state is left.
func (s *StateBuilder[S, E, D]) Exit(fn func(D) D) {
	s.state.Exit = append(s.state.Exit, fn)
	s.state.exitLabels = append(s.state.exitLabels, "")
}

// ExitLabel names the most recently appended exit action. Empty or repeated labels are invalid.
func (s *StateBuilder[S, E, D]) ExitLabel(name string) {
	s.labelAction("exit", s.state.exitLabels, name)
}

// EntryNamed appends an entry reducer and names it for diagrams.
func (s *StateBuilder[S, E, D]) EntryNamed(name string, fn func(D) D) {
	s.Entry(fn)
	s.EntryLabel(name)
}

// ExitNamed appends an exit reducer and names it for diagrams.
func (s *StateBuilder[S, E, D]) ExitNamed(name string, fn func(D) D) {
	s.Exit(fn)
	s.ExitLabel(name)
}

func (s *StateBuilder[S, E, D]) recordError(format string, args ...any) {
	s.machine.buildErrors = append(s.machine.buildErrors, fmt.Errorf("gstate: state %q: %s", s.state.ID, fmt.Sprintf(format, args...)))
}

func (s *StateBuilder[S, E, D]) labelAction(kind string, labels []string, name string) {
	if len(labels) == 0 || name == "" {
		s.recordError("%s label requires a preceding action and a nonempty name", kind)
		return
	}
	if labels[len(labels)-1] != "" {
		s.recordError("%s action already labeled %q; cannot assign %q", kind, labels[len(labels)-1], name)
		return
	}
	labels[len(labels)-1] = name
}

// Invoke configures an asynchronous service to run during the state's lifecycle.
//
// Parameters:
//   - fn: service function receiving ctx, entry snapshot, and mutate callback.
//     For details on the parameter contracts, see the documentation for InvokeDef.Func.
//   - onDone: state to transition to on success (when fn returns nil).
//   - onError: state to transition to on failure (when fn returns a non-nil error).
func (s *StateBuilder[S, E, D]) Invoke(fn func(ctx context.Context, snap D, mutate func(func(D) D)) error, onDone S, onError S) {
	if s.state.invokeSet {
		s.recordError("invoke already configured; cannot assign another service (use ReplaceInvoke)")
		return
	}
	s.state.invokeSet = true
	s.state.Invoke = &InvokeDef[S, E, D]{
		Func:    fn,
		OnDone:  onDone,
		OnError: onError,
	}
}

// ReplaceInvoke replaces an existing service identified by its nonempty label.
// Labels used for replacement are semantic contracts, not merely display text.
func (s *StateBuilder[S, E, D]) ReplaceInvoke(expectedLabel string, fn func(context.Context, D, func(func(D) D)) error, onDone, onError S) {
	if expectedLabel == "" || s.state.Invoke == nil || s.state.Invoke.label != expectedLabel {
		s.recordError("invoke replacement requires matching nonempty label %q", expectedLabel)
		return
	}
	s.state.Invoke = &InvokeDef[S, E, D]{Func: fn, OnDone: onDone, OnError: onError}
}

// InvokeLabel names the current service for diagrams and checked replacement.
// Missing services, empty names, and repeated labels are validation errors.
func (s *StateBuilder[S, E, D]) InvokeLabel(name string) {
	if s.state.Invoke == nil || name == "" {
		s.recordError("invoke label requires a preceding Invoke and a nonempty name")
		return
	}
	if s.state.Invoke.label != "" {
		s.recordError("invoke already labeled %q; cannot assign %q", s.state.Invoke.label, name)
		return
	}
	s.state.Invoke.label = name
}

// History enables history tracking for this state. Conflicting explicit modes
// are validation errors, including a change from an explicitly declared None.
func (s *StateBuilder[S, E, D]) History(t HistoryType) {
	if s.state.historySet && s.state.History != t {
		s.recordError("history already explicitly set to %v; cannot assign %v", s.state.History, t)
		return
	}
	s.state.History = t
	s.state.historySet = true
}

// On defines a transition triggered by a specific event.
func (s *StateBuilder[S, E, D]) On(event E) *TransitionBuilder[S, E, D] {
	t := &TransitionBuilder[S, E, D]{}
	if _, exists := s.state.Transitions[event]; !exists {
		s.state.eventOrder = append(s.state.eventOrder, event)
	}
	s.state.Transitions[event] = append(s.state.Transitions[event], &t.def)
	return t
}

// Always defines a transient transition that fires immediately if its guard passes.
func (s *StateBuilder[S, E, D]) Always() *TransitionBuilder[S, E, D] {
	t := &TransitionBuilder[S, E, D]{}
	s.state.Always = append(s.state.Always, &t.def)
	return t
}

// OnDone defines a local completion transition on a compound or parallel state.
// Every candidate must leave the source subtree and the last must be unconditional.
func (s *StateBuilder[S, E, D]) OnDone() *TransitionBuilder[S, E, D] {
	t := &TransitionBuilder[S, E, D]{}
	s.state.OnDone = append(s.state.OnDone, &t.def)
	return t
}

// ReplaceOnDone replaces a single unconditional completion route whose target
// matches expectedTarget. Configure the returned candidate and append any
// guarded alternatives with OnDone, ending with an unconditional fallback.
// A failed replacement records an error and returns a detached builder.
func (s *StateBuilder[S, E, D]) ReplaceOnDone(expectedTarget S) *TransitionBuilder[S, E, D] {
	t := &TransitionBuilder[S, E, D]{}
	routes := s.state.OnDone
	if len(routes) != 1 || routes[0] == nil || routes[0].Guard != nil || routes[0].Target != expectedTarget {
		s.recordError("OnDone replacement requires one unconditional route targeting %q", expectedTarget)
		return t
	}
	s.state.OnDone = []*TransitionDef[S, E, D]{&t.def}
	return t
}

// After defines a transition that fires automatically after a duration.
func (s *StateBuilder[S, E, D]) After(d time.Duration) *TransitionBuilder[S, E, D] {
	t := &TransitionBuilder[S, E, D]{def: TransitionDef[S, E, D]{After: d}}
	s.state.Delayed = append(s.state.Delayed, &t.def)
	return t
}

// TransitionBuilder provides a fluent API for configuring a state transition.
type TransitionBuilder[S ~string, E ~string, D Cloner[D]] struct {
	def TransitionDef[S, E, D]
}

// Guard adds a conditional check to the transition.
func (t *TransitionBuilder[S, E, D]) Guard(fn func(D) bool) *TransitionBuilder[S, E, D] {
	t.def.Guard = fn
	return t
}

// GuardLabel sets an optional label for the guard condition.
func (t *TransitionBuilder[S, E, D]) GuardLabel(name string) *TransitionBuilder[S, E, D] {
	t.def.guardName = name
	return t
}

// Assign appends a data update action to the transition. Repeated calls run in
// declaration order, each receiving the previous result. Nil actions are ignored.
// The composition emits one OnActionExecuted notification after all actions run.
func (t *TransitionBuilder[S, E, D]) Assign(fn func(D) D) *TransitionBuilder[S, E, D] {
	if fn == nil {
		return t
	}
	previous := t.def.Action
	if previous == nil {
		t.def.Action = fn
	} else {
		t.def.Action = func(data D) D { return fn(previous(data)) }
	}
	return t
}

// ActionLabel sets an optional label for the action.
func (t *TransitionBuilder[S, E, D]) ActionLabel(name string) *TransitionBuilder[S, E, D] {
	t.def.actionName = name
	return t
}

// Raise appends an event delivered to the external system when the
// transition fires. Repeated calls raise in declaration order. Raised events
// are handed to the handler installed with [Machine.WithRaiseHandler] after
// the transition commits and the actor lock is released; they are not
// delivered to the actor's own mailbox. Without a handler, raises are no-ops.
//
// A raise-only transition needs no target, so it is complete without GoTo:
//
//	s.After(30 * time.Second).Raise(EventTimeout)
//
// Combine with GoTo to raise while transitioning:
//
//	s.On(EventSubmit).Raise(EventSubmitted).GoTo(StateReview)
func (t *TransitionBuilder[S, E, D]) Raise(event E) *TransitionBuilder[S, E, D] {
	t.def.Raise = append(t.def.Raise, event)
	return t
}

// GoTo sets the target state for the transition.
func (t *TransitionBuilder[S, E, D]) GoTo(target S) {
	t.def.Target = target
}
