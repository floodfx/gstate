package gstate

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestInvokeMutateSettlesAndObserves(t *testing.T) {
	for _, final := range []bool{false, true} {
		t.Run(map[bool]string{false: "atomic", true: "final"}[final], func(t *testing.T) {
			release := make(chan struct{})
			mutated := make(chan struct{})
			rec := &RecordingObserver[StateID, EventID, Context]{}
			m := New[StateID, EventID, Context]("mutate").Initial("run").State("run", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Always().Guard(func(d Context) bool { return d.Count == 1 }).GoTo("end")
				s.Invoke(func(_ context.Context, _ Context, mutate func(func(Context) Context)) error {
					<-release
					mutate(func(d Context) Context { d.Count++; return d })
					// The first mutation exits run, so this retained callback is stale.
					mutate(func(d Context) Context { d.Count = 99; return d })
					close(mutated)
					return nil
				}, "", "")
			}).State("end", func(s *StateBuilder[StateID, EventID, Context]) {
				if final {
					s.Type(Final)
				}
			}).Build()
			a := Start(m, Context{}, m.WithObservers(rec))
			defer a.Stop()
			rec.Reset()
			close(release)
			<-mutated
			if a.State() != "end" || a.Data().Count != 1 {
				t.Fatalf("mutation did not settle: state=%s data=%+v", a.State(), a.Data())
			}
			actions := rec.Actions()
			if len(actions) != 1 {
				t.Fatalf("got %d action notifications, want 1", len(actions))
			}
			action := actions[0]
			if action.State != "run" || action.Target != "" || action.Event != "" || action.Data().Count != 1 {
				t.Fatalf("unexpected action: %+v", action)
			}
			transitions := rec.Transitions()
			if len(transitions) != 2 || transitions[0].From != "run" || transitions[0].To != "" || transitions[0].Event != "" || transitions[1].To != "end" {
				t.Fatalf("unexpected transitions: %+v", transitions)
			}
			if len(rec.EventsReceived()) != 0 || len(rec.EventsDropped()) != 0 {
				t.Fatal("mutate reported a mailbox event")
			}
			if final {
				select {
				case <-a.Done():
				case <-time.After(5 * time.Second):
					t.Fatal("mutation did not auto-stop")
				}
			}
		})
	}
}

func TestInvokeMutateAlwaysLimit(t *testing.T) {
	release := make(chan struct{})
	mutated := make(chan struct{})
	m := New[StateID, EventID, Context]("mutate-limit").Initial("run").State("run", func(s *StateBuilder[StateID, EventID, Context]) {
		s.Always().Guard(func(d Context) bool { return d.Count > 0 }).Assign(func(d Context) Context { d.Count++; return d })
		s.Invoke(func(_ context.Context, _ Context, mutate func(func(Context) Context)) error {
			<-release
			mutate(func(d Context) Context { d.Count = 1; return d })
			close(mutated)
			return nil
		}, "", "")
	}).Build()
	a := Start(m, Context{})
	defer a.Stop()
	close(release)
	<-mutated
	if !errors.Is(a.Err(), ErrAlwaysLimit) {
		t.Fatalf("Err = %v, want ErrAlwaysLimit", a.Err())
	}
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Always limit did not terminate actor")
	}
}
