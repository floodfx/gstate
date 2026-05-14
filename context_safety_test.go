package gstate

import (
	"context"
	"sync"
	"testing"
)

// mutatingObserver attempts to mutate Context through every payload it sees.
// If contextSnapshotPtr does its job, the actor's own context is unaffected.
type mutatingObserver struct {
	NopObserver[StateID, EventID, Context]
}

func (mutatingObserver) OnStateEntered(_ context.Context, e StateEvent[StateID, EventID, Context]) {
	if e.Context != nil {
		e.Context.Count = 9999
	}
}
func (mutatingObserver) OnStateExited(_ context.Context, e StateEvent[StateID, EventID, Context]) {
	if e.Context != nil {
		e.Context.Count = 9999
	}
}
func (mutatingObserver) OnTransition(_ context.Context, e TransitionEvent[StateID, EventID, Context]) {
	if e.Context != nil {
		e.Context.Count = 9999
	}
}
func (mutatingObserver) OnGuardEvaluated(_ context.Context, e GuardEvent[StateID, EventID, Context]) {
	if e.Context != nil {
		e.Context.Count = 9999
	}
}
func (mutatingObserver) OnActionExecuted(_ context.Context, e ActionEvent[StateID, EventID, Context]) {
	if e.Context != nil {
		e.Context.Count = 9999
	}
}

func TestObserverCannotMutateActorContext(t *testing.T) {
	// Machine that increments Count by 1 on GO.
	m := New[StateID, EventID, Context]("ctx-safety").
		Initial("a").
		State("a", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("GO").
				Guard(func(_ Context) bool { return true }).
				Assign(func(c Context) Context { c.Count++; return c }).
				GoTo("b")
		}).
		State("b", func(_ *StateBuilder[StateID, EventID, Context]) {}).
		Build()

	rec := &RecordingObserver[StateID, EventID, Context]{}
	bar := newKindBarrier(KindTransition, 1)
	a := Start(m, Context{Count: 1},
		m.WithObserver(
			MultiObserver[StateID, EventID, Context]{mutatingObserver{}, rec, bar},
		),
	)
	defer a.Stop()

	a.Send("GO")
	<-bar.done

	got := a.Context()
	if got.Count != 2 {
		t.Errorf("actor context Count = %d, want 2 (observer mutation must not leak through)", got.Count)
	}
}

// cloningContext implements Cloner so we can verify the Cloner path is used.
type cloningContext struct {
	Count   int
	cloned  *bool
	cloneMu *sync.Mutex
}

func (c cloningContext) Clone() cloningContext {
	c.cloneMu.Lock()
	*c.cloned = true
	c.cloneMu.Unlock()
	return c
}

type cState string
type cEvent string

func TestObserverUsesClonerWhenAvailable(t *testing.T) {
	cloned := false
	var mu sync.Mutex
	initial := cloningContext{Count: 1, cloned: &cloned, cloneMu: &mu}

	m := New[cState, cEvent, cloningContext]("cloner-ctx").
		Initial("a").
		State("a", func(s *StateBuilder[cState, cEvent, cloningContext]) {
			s.On("GO").GoTo("b")
		}).
		State("b", func(_ *StateBuilder[cState, cEvent, cloningContext]) {}).
		Build()

	rec := &RecordingObserver[cState, cEvent, cloningContext]{}
	barrier := make(chan struct{}, 1)
	signal := &cloneSignalObserver{ch: barrier}
	a := Start(m, initial, m.WithObserver(MultiObserver[cState, cEvent, cloningContext]{rec, signal}))
	defer a.Stop()
	a.Send("GO")

	<-barrier // deterministic: blocks until OnTransition fires

	mu.Lock()
	defer mu.Unlock()
	if !cloned {
		t.Error("Cloner.Clone() was never invoked; defensive copy did not take the Cloner path")
	}
}

type cloneSignalObserver struct {
	NopObserver[cState, cEvent, cloningContext]
	ch chan struct{}
}

func (c *cloneSignalObserver) OnTransition(context.Context, TransitionEvent[cState, cEvent, cloningContext]) {
	select {
	case c.ch <- struct{}{}:
	default:
	}
}

