package gstate

import (
	"context"
	"sync"
	"testing"
	"time"
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
	a := Start(m, Context{Count: 1},
		WithObserver[StateID, EventID, Context](
			multiObserver{mutatingObserver{}, rec},
		),
	)
	defer a.Stop()

	a.Send("GO")
	// Wait until the transition has been observed.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && len(rec.Transitions()) == 0 {
		time.Sleep(time.Millisecond)
	}

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

	a := Start(m, initial, WithObserver[cState, cEvent, cloningContext](&RecordingObserver[cState, cEvent, cloningContext]{}))
	defer a.Stop()
	a.Send("GO")
	time.Sleep(20 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if !cloned {
		t.Error("Cloner.Clone() was never invoked; defensive copy did not take the Cloner path")
	}
}

// multiObserver fans Observer callbacks to multiple targets (mirrors the
// helper in examples/observer/main.go).
type multiObserver []Observer[StateID, EventID, Context]

func (m multiObserver) OnTransition(ctx context.Context, e TransitionEvent[StateID, EventID, Context]) {
	for _, o := range m {
		o.OnTransition(ctx, e)
	}
}
func (m multiObserver) OnGuardEvaluated(ctx context.Context, e GuardEvent[StateID, EventID, Context]) {
	for _, o := range m {
		o.OnGuardEvaluated(ctx, e)
	}
}
func (m multiObserver) OnInvokeStarted(ctx context.Context, e InvokeEvent[StateID, EventID, Context]) {
	for _, o := range m {
		o.OnInvokeStarted(ctx, e)
	}
}
func (m multiObserver) OnInvokeCompleted(ctx context.Context, e InvokeEvent[StateID, EventID, Context]) {
	for _, o := range m {
		o.OnInvokeCompleted(ctx, e)
	}
}
func (m multiObserver) OnStateEntered(ctx context.Context, e StateEvent[StateID, EventID, Context]) {
	for _, o := range m {
		o.OnStateEntered(ctx, e)
	}
}
func (m multiObserver) OnStateExited(ctx context.Context, e StateEvent[StateID, EventID, Context]) {
	for _, o := range m {
		o.OnStateExited(ctx, e)
	}
}
func (m multiObserver) OnActionExecuted(ctx context.Context, e ActionEvent[StateID, EventID, Context]) {
	for _, o := range m {
		o.OnActionExecuted(ctx, e)
	}
}
func (m multiObserver) OnEventReceived(ctx context.Context, e EventNotice[StateID, EventID, Context]) {
	for _, o := range m {
		o.OnEventReceived(ctx, e)
	}
}
func (m multiObserver) OnEventDropped(ctx context.Context, e EventNotice[StateID, EventID, Context]) {
	for _, o := range m {
		o.OnEventDropped(ctx, e)
	}
}
