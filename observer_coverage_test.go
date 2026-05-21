package gstate

import (
	"context"
	"testing"
	"time"
)

// TestOnTransitionFiresOncePerEventTransition asserts an event-driven
// transition emits exactly one OnTransition.
func TestOnTransitionFiresOncePerEventTransition(t *testing.T) {
	rec := &RecordingObserver[StateID, EventID, Context]{}
	bar := newKindBarrier(KindTransition, 1)
	m := tinyMachine()
	a := Start(m, Context{}, m.WithObservers(rec, bar))
	defer a.Stop()

	a.Send("GO")
	<-bar.done

	if got := len(rec.Transitions()); got != 1 {
		t.Errorf("OnTransition fired %d times, want 1", got)
	}
}

// TestOnTransitionFiresOncePerAlwaysTransition asserts an Always-driven
// transient transition emits exactly one OnTransition.
func TestOnTransitionFiresOncePerAlwaysTransition(t *testing.T) {
	rec := &RecordingObserver[StateID, EventID, Context]{}
	bar := newKindBarrier(KindTransition, 1)
	m := New[StateID, EventID, Context]("always_once").
		Initial("a").
		State("a", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Always().Guard(func(_ Context) bool { return true }).GoTo("b")
		}).
		State("b", func(_ *StateBuilder[StateID, EventID, Context]) {}).
		Build()

	a := Start(m, Context{}, m.WithObservers(rec, bar))
	defer a.Stop()
	<-bar.done

	if got := len(rec.Transitions()); got != 1 {
		t.Errorf("Always OnTransition fired %d times, want 1", got)
	}
}

// TestOnTransitionFiresOncePerDelayedTransition asserts a delayed transition
// emits exactly one OnTransition.
func TestOnTransitionFiresOncePerDelayedTransition(t *testing.T) {
	rec := &RecordingObserver[StateID, EventID, Context]{}
	bar := newKindBarrier(KindTransition, 1)
	m := New[StateID, EventID, Context]("delayed_once").
		Initial("a").
		State("a", func(s *StateBuilder[StateID, EventID, Context]) {
			s.After(time.Millisecond).GoTo("b")
		}).
		State("b", func(_ *StateBuilder[StateID, EventID, Context]) {}).
		Build()

	a := Start(m, Context{}, m.WithObservers(rec, bar))
	defer a.Stop()
	<-bar.done

	if got := len(rec.Transitions()); got != 1 {
		t.Errorf("Delayed OnTransition fired %d times, want 1", got)
	}
}

// TestOnTransitionFiresOncePerInvokeCompletion asserts an invoke-completion
// transition emits exactly one OnTransition.
func TestOnTransitionFiresOncePerInvokeCompletion(t *testing.T) {
	rec := &RecordingObserver[StateID, EventID, Context]{}
	bar := newKindBarrier(KindTransition, 1)
	m := New[StateID, EventID, Context]("invoke_once").
		Initial("loading").
		State("loading", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Invoke(func(_ context.Context, _ Context, _ func(func(Context) Context)) error { return nil }, "done", "fail")
		}).
		State("done", func(s *StateBuilder[StateID, EventID, Context]) { s.Type(Final) }).
		State("fail", func(s *StateBuilder[StateID, EventID, Context]) { s.Type(Final) }).
		Build()

	a := Start(m, Context{}, m.WithObservers(rec, bar))
	defer a.Stop()
	<-bar.done

	if got := len(rec.Transitions()); got != 1 {
		t.Errorf("Invoke-completion OnTransition fired %d times, want 1", got)
	}
}

// TestSelfTransitionEmitsExitAndEntry asserts a self-transition fires one
// OnStateExited and one OnStateEntered for the source state. This is the
// observer-side counterpart of the existing TestSelfTransitionReEntry which
// only checks Entry/Exit handler counters.
func TestSelfTransitionEmitsExitAndEntry(t *testing.T) {
	rec := &RecordingObserver[StateID, EventID, Context]{}
	bar := newKindBarrier(KindTransition, 1)
	m := New[StateID, EventID, Context]("self_obs").
		Initial("active").
		State("active", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("RETRY").GoTo("active")
		}).
		Build()

	a := Start(m, Context{}, m.WithObservers(rec, bar))
	defer a.Stop()

	a.Send("RETRY")
	<-bar.done

	exited := rec.StateExited()
	entered := rec.StateEntered()
	// Pre-Send: one StateEntered for initial entry.
	// Post-Send: one StateExited (active) + one StateEntered (active) for the
	// self-transition.
	if len(exited) != 1 || exited[0].State != "active" {
		t.Errorf("StateExited: got %+v, want one entry for 'active'", exited)
	}
	if len(entered) != 2 || entered[0].State != "active" || entered[1].State != "active" {
		t.Errorf("StateEntered: got %+v, want two entries for 'active'", entered)
	}

	transitions := rec.Transitions()
	if len(transitions) != 1 {
		t.Fatalf("Transitions: got %d, want 1", len(transitions))
	}
	if transitions[0].From != "active" || transitions[0].To != "active" || transitions[0].Event != "RETRY" {
		t.Errorf("Transition payload = %+v", transitions[0])
	}
}

func parallelMachine() *Machine[StateID, EventID, Context] {
	return New[StateID, EventID, Context]("parallel_obs").
		Initial("parent").
		State("parent", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Type(Parallel)
			s.State("r1", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Initial("a")
				s.State("a", func(s *StateBuilder[StateID, EventID, Context]) {
					s.On("GO").GoTo("b")
				})
				s.State("b", func(_ *StateBuilder[StateID, EventID, Context]) {})
			})
			s.State("r2", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Initial("c")
				s.State("c", func(_ *StateBuilder[StateID, EventID, Context]) {})
			})
		}).
		Build()
}

// TestParallelRegionsEmitCountsForBothRegionsOnEntry asserts that on initial
// entry into a Parallel state, OnStateEntered fires for the parent and for
// each region's active leaf. Initial entry fires 5 OnStateEntered events:
// parent, r1, a, r2, c.
func TestParallelRegionsEmitCountsForBothRegionsOnEntry(t *testing.T) {
	rec := &RecordingObserver[StateID, EventID, Context]{}
	bar := newKindBarrier(KindStateEntered, 5)
	m := parallelMachine()

	a := Start(m, Context{}, m.WithObservers(rec, bar))
	defer a.Stop()
	<-bar.done

	entered := rec.StateEntered()
	got := map[StateID]int{}
	for _, e := range entered {
		got[e.State]++
	}
	for _, want := range []StateID{"parent", "r1", "a", "r2", "c"} {
		if got[want] != 1 {
			t.Errorf("OnStateEntered for %q fired %d times, want 1 (full: %v)", want, got[want], got)
		}
	}
}

// TestParallelRegionEventTransitionFiresOneTransition asserts that an event
// affecting one region of a Parallel parent fires a single OnTransition for
// that region, not one per region.
func TestParallelRegionEventTransitionFiresOneTransition(t *testing.T) {
	rec := &RecordingObserver[StateID, EventID, Context]{}
	entryBar := newKindBarrier(KindStateEntered, 5)
	transBar := newKindBarrier(KindTransition, 1)
	m := parallelMachine()

	a := Start(m, Context{}, m.WithObservers(rec, entryBar, transBar))
	defer a.Stop()
	<-entryBar.done

	a.Send("GO")
	<-transBar.done

	transitions := rec.Transitions()
	if len(transitions) != 1 {
		t.Fatalf("Transitions: got %d, want 1 (full: %+v)", len(transitions), transitions)
	}
	if transitions[0].From != "a" || transitions[0].To != "b" {
		t.Errorf("Transition payload = %+v", transitions[0])
	}
}
