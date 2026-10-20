package gstate

import (
	"context"
	"testing"
	"time"
)

func TestActorAfter(t *testing.T) {
	// A real timer duration is the behavior under test; the transition callback
	// deterministically signals completion.
	done := make(chan struct{})
	m := New[StateID, EventID, Context]("after").
		Initial("idle").
		State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
			s.After(time.Millisecond).GoTo("timeout")
		}).
		State("timeout", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Type(Final)
		}).
		Build()

	obs := ObserverFuncs[StateID, EventID, Context]{
		TransitionFunc: func(context.Context, *TransitionEvent[StateID, EventID, Context]) {
			close(done)
		},
	}
	actor := Start(m, Context{}, m.WithObservers(obs))
	defer actor.Stop()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for delayed transition")
	}

	if actor.State() != "timeout" {
		t.Errorf("Expected state timeout after delay, got %s", actor.State())
	}
}

func TestDelayedGuardAndAction(t *testing.T) {
	// A real timer duration is the behavior under test; observer callbacks
	// deterministically signal guard evaluation or transition completion.
	t.Run("false_guard_blocks_transition", func(t *testing.T) {
		guardEvaluated := make(chan *GuardEvent[StateID, EventID, Context], 1)
		calls := 0
		m := New[StateID, EventID, Context]("delay-false-guard").
			Initial("a").
			State("a", func(s *StateBuilder[StateID, EventID, Context]) {
				s.After(time.Nanosecond).
					Guard(func(Context) bool { calls++; return false }).
					Assign(func(c Context) Context { c.Count++; return c }).
					GoTo("b")
			}).
			State("b", func(_ *StateBuilder[StateID, EventID, Context]) {}).
			Build()

		obs := ObserverFuncs[StateID, EventID, Context]{
			GuardEvaluatedFunc: func(_ context.Context, e *GuardEvent[StateID, EventID, Context]) {
				guardEvaluated <- e
			},
			TransitionFunc: func(_ context.Context, e *TransitionEvent[StateID, EventID, Context]) {
				t.Errorf("unexpected transition when guard is false: %v", e)
			},
		}
		a := Start(m, Context{}, m.WithObservers(obs))
		defer a.Stop()

		select {
		case ge := <-guardEvaluated:
			if ge.Result {
				t.Errorf("expected guard Result=false, got true")
			}
			if ge.State != "a" || ge.Target != "b" {
				t.Errorf("guard event = %+v, want State=a Target=b", ge)
			}
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for delayed guard evaluation")
		}

		data := a.Data()
		if calls != 1 {
			t.Errorf("guard calls=%d, want 1", calls)
		}
		if a.State() != "a" {
			t.Errorf("false guard reached state %q, want \"a\"", a.State())
		}
		if data.Count != 0 {
			t.Errorf("action executed despite false guard: Count=%d, want 0", data.Count)
		}
	})

	t.Run("true_guard_and_action_execute", func(t *testing.T) {
		done := make(chan struct{})
		calls := 0
		rec := &RecordingObserver[StateID, EventID, Context]{}
		m := New[StateID, EventID, Context]("delay-true-guard").
			Initial("a").
			State("a", func(s *StateBuilder[StateID, EventID, Context]) {
				s.After(time.Nanosecond).
					Guard(func(Context) bool { calls++; return true }).
					Assign(func(c Context) Context { c.Count += 7; return c }).
					GoTo("b")
			}).
			State("b", func(_ *StateBuilder[StateID, EventID, Context]) {}).
			Build()

		obs := ObserverFuncs[StateID, EventID, Context]{
			TransitionFunc: func(context.Context, *TransitionEvent[StateID, EventID, Context]) {
				close(done)
			},
		}
		a := Start(m, Context{}, m.WithObservers(rec, obs))
		defer a.Stop()

		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for delayed transition")
		}

		data := a.Data()
		if calls != 1 {
			t.Errorf("guard calls=%d, want 1", calls)
		}
		if a.State() != "b" {
			t.Errorf("state=%q, want \"b\"", a.State())
		}
		if data.Count != 7 {
			t.Errorf("action Count=%d, want 7", data.Count)
		}
		if got := rec.Guards(); len(got) != 1 || !got[0].Result {
			t.Errorf("expected 1 true guard event, got %+v", got)
		}
		if got := rec.Actions(); len(got) != 1 || got[0].State != "a" || got[0].Target != "b" {
			t.Errorf("expected 1 action event on a->b, got %+v", got)
		}
	})

	t.Run("targetless_delayed_action", func(t *testing.T) {
		done := make(chan struct{})
		rec := &RecordingObserver[StateID, EventID, Context]{}
		m := New[StateID, EventID, Context]("delay-internal").
			Initial("a").
			State("a", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Entry(func(c Context) Context { c.Count += 100; return c })
				s.After(time.Nanosecond).
					Assign(func(c Context) Context { c.Count += 5; return c })
			}).
			Build()

		obs := ObserverFuncs[StateID, EventID, Context]{
			TransitionFunc: func(context.Context, *TransitionEvent[StateID, EventID, Context]) {
				close(done)
			},
		}
		a := Start(m, Context{}, m.WithObservers(rec, obs))
		defer a.Stop()

		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for targetless delayed transition")
		}

		// Entry ran once (+100), internal delayed action ran once (+5), no re-entry.
		if got := a.Data().Count; got != 105 {
			t.Errorf("Data().Count = %d, want 105", got)
		}
		if got := rec.Actions(); len(got) != 1 || got[0].Target != "" {
			t.Errorf("expected 1 internal action event, got %+v", got)
		}
	})
}
