package gstate

import "testing"

func TestAssignComposesInDeclarationOrder(t *testing.T) {
	for _, kind := range []string{"internal", "external", "always"} {
		t.Run(kind, func(t *testing.T) {
			rec := &RecordingObserver[StateID, EventID, Context]{}
			barrier := newKindBarrier(KindTransition, 1)
			m := New[StateID, EventID, Context]("assign").Initial("a").State("a", func(s *StateBuilder[StateID, EventID, Context]) {
				var tr *TransitionBuilder[StateID, EventID, Context]
				if kind == "always" {
					tr = s.Always().Guard(func(d Context) bool { return d.Count == 0 })
				} else {
					tr = s.On("GO")
				}
				tr.Assign(func(d Context) Context { d.Count += 2; return d }).Assign(nil).Assign(func(d Context) Context { d.Count *= 3; return d })
				if kind == "external" {
					tr.GoTo("b")
				}
			}).State("b", func(s *StateBuilder[StateID, EventID, Context]) {}).Build()
			a := Start(m, Context{}, m.WithObservers(rec, barrier))
			defer a.Stop()
			if kind != "always" {
				a.Send("GO")
			}
			<-barrier.done
			if got := a.Data().Count; got != 6 {
				t.Fatalf("Count = %d, want 6", got)
			}
			actions := rec.Actions()
			if len(actions) != 1 || actions[0].Data().Count != 6 {
				t.Fatalf("composed action notifications = %+v", actions)
			}
			if kind == "external" && a.State() != "b" {
				t.Fatal("external transition did not enter target")
			}
		})
	}
}
