package gstate

import (
	"context"
	"testing"
	"time"
)

// A real timer callback is held behind the actor lock. Its channel announces
// delivery before re-entry or shutdown changes the generation under that lock.
// A channel replaces all scheduling delays, but not the timer trigger itself.
func TestServiceDeliveryAcrossLockedLifecycleChange(t *testing.T) {
	for _, mode := range []string{"reentry", "shutdown"} {
		t.Run(mode, func(t *testing.T) {
			guards, actions, entries, starts := 0, 0, 0, 0
			m := New[string, string, Context]("queued-service").Initial("a").State("a", func(s *StateBuilder[string, string, Context]) {
				s.Entry(func(c Context) Context { c.Count++; return c })
				s.Invoke(func(ctx context.Context, _ Context, _ func(func(Context) Context)) error {
					<-ctx.Done()
					return ctx.Err()
				}, "", "")
			}).State("b", func(s *StateBuilder[string, string, Context]) {
				s.Entry(func(c Context) Context { entries++; return c })
				s.Invoke(func(context.Context, Context, func(func(Context) Context)) error { return nil }, "", "")
			}).Build()
			a := Start(m, Context{}, m.WithObservers(ObserverFuncs[string, string, Context]{InvokeStartedFunc: func(_ context.Context, e *InvokeEvent[string, string, Context]) {
				if e.State == "b" {
					starts++
				}
			}}))
			defer a.Stop()
			a.mu.Lock()
			generation := a.stateGens["a"]
			attempted, finished := make(chan struct{}), make(chan struct{})
			timer := time.AfterFunc(0, func() {
				close(attempted)
				a.executeInternalTransition(context.Background(), "a", generation, &TransitionDef[string, string, Context]{
					Guard: func(Context) bool { guards++; return true }, Action: func(c Context) Context { actions++; return c }, Target: "b",
				})
				close(finished)
			})
			<-attempted
			wantCount := 1
			if mode == "reentry" {
				a.executeTransition(context.Background(), "a", &TransitionDef[string, string, Context]{Target: "a"}, "REENTER")
				wantCount = 2
			} else {
				a.initiateShutdownLocked()
			}
			a.mu.Unlock()
			select {
			case <-finished:
			case <-time.After(5 * time.Second):
				t.Fatal("service delivery did not finish")
			}
			timer.Stop()
			if guards != 0 || actions != 0 || entries != 0 || starts != 0 {
				t.Fatalf("stale delivery effects: guards=%d actions=%d entries=%d starts=%d", guards, actions, entries, starts)
			}
			if a.State() != "a" || a.Data().Count != wantCount {
				t.Fatalf("configuration changed: states=%v data=%+v", a.States(), a.Data())
			}
		})
	}
}

func TestDuplicateAncestorIDFailsDuringDeclaration(t *testing.T) {
	assertPanic(t, func() {
		New[string, string, Context]("ancestor-duplicate").Initial("a").State("a", func(s *StateBuilder[string, string, Context]) {
			s.Initial("a")
			s.State("a", func(*StateBuilder[string, string, Context]) {})
		}).Build()
	}, "gstate: duplicate state ID")
}
