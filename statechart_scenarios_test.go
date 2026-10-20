package gstate

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

// processingAck signals from inside the actor lock. Acquiring States() after
// receiving the signal waits for the whole event, including Always, to finish.
type processingAck[S ~string, E ~string, D Cloner[D]] struct {
	BaseObserver[S, E, D]
	received chan struct{}
}

func (o processingAck[S, E, D]) OnEventReceived(context.Context, *EventNotice[S, E, D]) {
	o.received <- struct{}{}
}
func sendProcessed[S ~string, E ~string, D Cloner[D]](t testing.TB, a *Actor[S, E, D], ack <-chan struct{}, event E) bool {
	t.Helper()
	if err := a.SendCtx(context.Background(), event); err != nil {
		if errors.Is(err, ErrActorStopped) {
			return false
		}
		t.Fatalf("SendCtx(%q): %v", event, err)
	}
	select {
	case <-ack:
	case <-time.After(5 * time.Second):
		t.Fatalf("event %q not received", event)
	}
	assertActiveConfiguration(t, a.machine, a.States())
	return true
}

func assertActiveConfiguration[S ~string, E ~string, D Cloner[D]](t testing.TB, m *Machine[S, E, D], states []S) {
	t.Helper()
	active := map[S]bool{}
	for _, id := range states {
		if active[id] {
			t.Fatalf("duplicate active state %q", id)
		}
		active[id] = true
	}
	roots := 0
	for _, id := range states {
		sd := m.States[id]
		if sd == nil {
			t.Fatalf("unknown state %q", id)
		}
		if sd.parent == "" {
			roots++
		} else if !active[sd.parent] {
			t.Fatalf("%q lacks active parent %q", id, sd.parent)
		}
		count := 0
		for child := range sd.States {
			if active[child] {
				count++
			}
		}
		switch sd.Type {
		case Compound:
			if count != 1 {
				t.Fatalf("compound %q has %d active children: %v", id, count, states)
			}
		case Parallel:
			if count != len(sd.States) {
				t.Fatalf("parallel %q missing regions: %v", id, states)
			}
		}
	}
	if roots != 1 {
		t.Fatalf("active top-level states=%d: %v", roots, states)
	}
}

func TestStatechartScenarios(t *testing.T) {
	type B = StateBuilder[StateID, EventID, Context]
	type M = Machine[StateID, EventID, Context]
	type step struct {
		event EventID
		want  []StateID
		count int
	}
	cases := []struct {
		name    string
		machine func() *M
		steps   []step
	}{
		{"atomic guards actions and targetless", func() *M {
			return New[StateID, EventID, Context]("atomic").Initial("a").State("a", func(s *B) {
				s.On("GO").Guard(func(c Context) bool { return c.Count > 0 }).GoTo("b")
				s.On("INC").Assign(func(c Context) Context { c.Count++; return c })
			}).State("b", func(s *B) { s.Type(Final) }).Build()
		}, []step{{"GO", []StateID{"a"}, 0}, {"INC", []StateID{"a"}, 1}, {"GO", []StateID{"b"}, 1}}},
		{"compound and Always", func() *M {
			return New[StateID, EventID, Context]("compound").Initial("p").State("p", func(s *B) {
				s.Initial("a")
				s.State("a", func(s *B) { s.On("GO").GoTo("transient") })
				s.State("transient", func(s *B) { s.Always().Assign(func(c Context) Context { c.Count += 2; return c }).GoTo("b") })
				s.State("b", func(*B) {})
			}).Build()
		}, []step{{"GO", []StateID{"p", "b"}, 2}, {"UNKNOWN", []StateID{"p", "b"}, 2}}},
		{"parallel broadcast", func() *M {
			return New[StateID, EventID, Context]("parallel").Initial("p").State("p", func(s *B) {
				s.Type(Parallel)
				s.State("left", func(s *B) { s.On("GO").Assign(func(c Context) Context { c.Count++; return c }) })
				s.State("right", func(s *B) { s.On("GO").Assign(func(c Context) Context { c.Count += 10; return c }) })
			}).Build()
		}, []step{{"GO", []StateID{"p", "left", "right"}, 11}}},
	}
	for _, history := range []HistoryType{Shallow, Deep} {
		name := "shallow history"
		leaf := StateID("a")
		if history == Deep {
			name = "deep history"
			leaf = "b"
		}
		cases = append(cases, struct {
			name    string
			machine func() *M
			steps   []step
		}{name, func() *M {
			return New[StateID, EventID, Context]("history").Initial("p").State("p", func(s *B) {
				s.History(history)
				s.Initial("q")
				s.On("LEAVE").GoTo("out")
				s.State("q", func(s *B) {
					s.Initial("a")
					s.State("a", func(s *B) { s.On("GO").GoTo("b") })
					s.State("b", func(*B) {})
				})
			}).State("out", func(s *B) { s.On("BACK").GoTo("p") }).Build()
		}, []step{{"GO", []StateID{"p", "q", "b"}, 0}, {"LEAVE", []StateID{"out"}, 0}, {"BACK", []StateID{"p", "q", leaf}, 0}}})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.machine()
			ack := processingAck[StateID, EventID, Context]{received: make(chan struct{}, 1)}
			a := Start(m, Context{}, m.WithObservers(ack))
			defer a.Stop()
			assertActiveConfiguration(t, m, a.States())
			for _, step := range tc.steps {
				if !sendProcessed(t, a, ack.received, step.event) {
					t.Fatalf("stopped before %q", step.event)
				}
				if got := a.States(); !slices.Equal(got, step.want) {
					t.Fatalf("after %q: states=%v, want %v", step.event, got, step.want)
				}
				if got := a.Data().Count; got != step.count {
					t.Fatalf("after %q: count=%d, want %d", step.event, got, step.count)
				}
				// The same logical configuration must survive a persistence round trip.
				restored := Hydrate(m, a.Snapshot())
				assertActiveConfiguration(t, m, restored.States())
				if got := restored.States(); !slices.Equal(got, step.want) {
					t.Errorf("restored states=%v", got)
				}
				restored.Stop()
			}
		})
	}
}

// Real delays exercise the timer trigger; completion is synchronized by a hook.
func TestServiceCompletionScenarios(t *testing.T) {
	for _, trigger := range []string{"timer", "invoke"} {
		t.Run(trigger, func(t *testing.T) {
			done := make(chan struct{}, 1)
			m := New[string, string, Context]("services").Initial("working").State("working", func(s *StateBuilder[string, string, Context]) {
				if trigger == "timer" {
					s.After(time.Nanosecond).Guard(func(Context) bool { return true }).Assign(func(c Context) Context { c.Count = 7; return c }).GoTo("done")
				} else {
					s.Invoke(func(_ context.Context, _ Context, mutate func(func(Context) Context)) error {
						mutate(func(c Context) Context { c.Count = 7; return c })
						return nil
					}, "done", "")
				}
			}).State("done", func(s *StateBuilder[string, string, Context]) { s.Type(Final) }).Build()
			a := Start(m, Context{}, m.WithObservers(ObserverFuncs[string, string, Context]{TransitionFunc: func(context.Context, *TransitionEvent[string, string, Context]) { done <- struct{}{} }}))
			defer a.Stop()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("service completion missing")
			}
			if got := a.States(); !slices.Equal(got, []string{"done"}) {
				t.Fatalf("states=%v", got)
			}
			if a.Data().Count != 7 {
				t.Fatal("service action missing")
			}
			if !errors.Is(a.SendCtx(context.Background(), "AFTER"), ErrActorStopped) {
				t.Fatal("Final did not stop actor")
			}
			assertActiveConfiguration(t, m, a.States())
		})
	}
}
