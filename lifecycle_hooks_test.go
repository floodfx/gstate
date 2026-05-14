package gstate

import (
	"testing"
	"time"
)

// guardedMachine builds a tiny machine with a guarded transition that has an action.
// State graph: a --GO[guard=allow]/inc--> b; sending GO when allow=false drops.
func guardedMachine(allow bool) *Machine[StateID, EventID, Context] {
	return New[StateID, EventID, Context]("guarded").
		Initial("a").
		State("a", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Entry(func(c Context) Context { return c })
			s.Exit(func(c Context) Context { return c })
			s.On("GO").
				Guard(func(_ Context) bool { return allow }).
				Assign(func(c Context) Context { c.Count++; return c }).
				GoTo("b")
		}).
		State("b", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Entry(func(c Context) Context { return c })
		}).
		Build()
}

func waitForKind(t *testing.T, rec *RecordingObserver[StateID, EventID, Context], kind string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(rec.Events(kind)) > 0 {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s event", kind)
}

func TestLifecycleHooksHappyPathOrder(t *testing.T) {
	rec := &RecordingObserver[StateID, EventID, Context]{}
	m := guardedMachine(true)
	a := Start(m, Context{}, WithObserver[StateID, EventID, Context](rec))
	defer a.Stop()

	// initial entry of "a" should have fired before Send.
	waitForKind(t, rec, KindStateEntered, time.Second)

	a.Send("GO")
	waitForKind(t, rec, KindTransition, time.Second)

	// Sequence after Send (entry of "a" already happened pre-Send):
	// EventReceived, GuardEvaluated(true), StateExited(a), ActionExecuted, StateEntered(b), Transition
	all := rec.Events()
	// Find the EventReceived for "GO" and assert the post-trigger sequence.
	var idx int = -1
	for i, ev := range all {
		if ev.Kind == KindEventReceived {
			n := ev.Payload.(EventNotice[StateID, EventID, Context])
			if n.Event == "GO" {
				idx = i
				break
			}
		}
	}
	if idx < 0 {
		t.Fatal("no EventReceived for GO recorded")
	}
	want := []string{
		KindEventReceived,
		KindGuardEvaluated,
		KindStateExited,
		KindActionExecuted,
		KindStateEntered,
		KindTransition,
	}
	if len(all)-idx < len(want) {
		t.Fatalf("not enough events after EventReceived: have %d more, need %d", len(all)-idx-1, len(want)-1)
	}
	for i, w := range want {
		if all[idx+i].Kind != w {
			t.Errorf("event %d after GO: got %s, want %s (full: %v)", i, all[idx+i].Kind, w, kinds(all[idx:idx+len(want)]))
		}
	}

	if got := rec.Guards(); len(got) == 0 || !got[len(got)-1].Result {
		t.Errorf("expected guard Result=true; got %+v", got)
	}
	if got := rec.Transitions(); len(got) == 0 {
		t.Fatal("no transitions recorded")
	} else {
		last := got[len(got)-1]
		if last.From != "a" || last.To != "b" || last.Event != "GO" {
			t.Errorf("transition payload = %+v", last)
		}
	}
	if got := rec.Actions(); len(got) != 1 || got[0].Target != "b" || got[0].Event != "GO" {
		t.Errorf("action payload unexpected: %+v", got)
	}
}

func kinds(evs []RecordedEvent) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.Kind
	}
	return out
}

func TestGuardFailEmitsFalseAndDoesNotTransition(t *testing.T) {
	rec := &RecordingObserver[StateID, EventID, Context]{}
	m := guardedMachine(false)
	a := Start(m, Context{}, WithObserver[StateID, EventID, Context](rec))
	defer a.Stop()
	waitForKind(t, rec, KindStateEntered, time.Second)

	a.Send("GO")
	waitForKind(t, rec, KindEventDropped, time.Second)

	if got := rec.Guards(); len(got) != 1 || got[0].Result {
		t.Errorf("expected single guard with Result=false; got %+v", got)
	}
	if got := rec.Transitions(); len(got) != 0 {
		t.Errorf("expected no transitions; got %+v", got)
	}
	if got := rec.EventsDropped(); len(got) != 1 || got[0].Reason != "no_transition" {
		t.Errorf("expected one drop with reason no_transition; got %+v", got)
	}
}

func TestEventDroppedOnUnknownEvent(t *testing.T) {
	rec := &RecordingObserver[StateID, EventID, Context]{}
	m := guardedMachine(true)
	a := Start(m, Context{}, WithObserver[StateID, EventID, Context](rec))
	defer a.Stop()
	waitForKind(t, rec, KindStateEntered, time.Second)

	a.Send("UNKNOWN")
	waitForKind(t, rec, KindEventDropped, time.Second)

	if got := rec.EventsDropped(); len(got) != 1 || got[0].Event != "UNKNOWN" {
		t.Errorf("dropped: %+v", got)
	}
}
