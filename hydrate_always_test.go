package gstate

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestHydrateStabilizesAlways(t *testing.T) {
	type B = StateBuilder[string, string, Context]
	for _, tc := range []struct {
		name   string
		active []string
		final  bool
	}{
		{"empty", nil, false},
		{"unknown", []string{"missing"}, false},
		{"partial compound", []string{"root"}, false},
		{"restored decider", []string{"decide"}, false},
		{"completion", []string{"root"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var actions, entered, transitions []string
			m := New[string, string, Context]("hydrate-always").Initial("root").State("root", func(s *B) {
				s.Initial("decide")
				s.Entry(func(c Context) Context { actions = append(actions, "root entry"); return c })
				s.State("decide", func(s *B) {
					s.Entry(func(c Context) Context { actions = append(actions, "decide entry"); return c })
					s.Exit(func(c Context) Context { actions = append(actions, "decide exit"); return c })
					s.Always().Guard(func(c Context) bool { return c.Count == 0 }).Assign(func(c Context) Context {
						actions = append(actions, "internal")
						c.Count++
						return c
					})
					s.Always().Guard(func(c Context) bool { return c.Count == 1 }).Assign(func(c Context) Context {
						actions = append(actions, "targeted")
						c.Count++
						return c
					}).GoTo("settled")
				})
				s.State("settled", func(s *B) {
					if tc.final {
						s.Type(Final)
					}
					s.Entry(func(c Context) Context { actions = append(actions, "settled entry"); return c })
				})
			}).Build()
			a := Hydrate(m, Snapshot[string, Context]{Active: tc.active}, m.WithObservers(ObserverFuncs[string, string, Context]{
				StateEnteredFunc: func(_ context.Context, e *StateEvent[string, string, Context]) { entered = append(entered, e.State) },
				TransitionFunc: func(_ context.Context, e *TransitionEvent[string, string, Context]) {
					transitions = append(transitions, e.From+">"+e.To)
				},
			}))
			defer a.Stop()
			if got := a.States(); !slices.Equal(got, []string{"root", "settled"}) {
				t.Fatalf("states=%v, want [root settled] before Hydrate returns", got)
			}
			if got := a.Data().Count; got != 2 {
				t.Errorf("Count=%d, want 2", got)
			}
			if !slices.Equal(actions, []string{"internal", "decide exit", "targeted", "settled entry"}) {
				t.Errorf("actions=%v", actions)
			}
			if !slices.Equal(entered, []string{"settled"}) {
				t.Errorf("entered=%v, want only newly entered settled", entered)
			}
			if !slices.Equal(transitions, []string{"decide>", "decide>settled"}) {
				t.Errorf("transitions=%v", transitions)
			}
			err := a.SendCtx(context.Background(), "unused")
			if tc.final && !errors.Is(err, ErrActorStopped) {
				t.Errorf("SendCtx=%v, want ErrActorStopped", err)
			}
			if !tc.final && err != nil {
				t.Errorf("SendCtx=%v, want running actor", err)
			}
			if a.Err() != nil {
				t.Errorf("Err()=%v", a.Err())
			}
		})
	}
}

func TestHydrateAlwaysExhaustionStopsServices(t *testing.T) {
	cancelled := make(chan struct{})
	m := New[string, string, Context]("hydrate-cycle").Initial("a").State("a", func(s *StateBuilder[string, string, Context]) {
		s.Invoke(func(ctx context.Context, _ Context, _ func(func(Context) Context)) error {
			<-ctx.Done()
			close(cancelled)
			return nil
		}, "", "")
		s.Always().Assign(func(c Context) Context { c.Count++; return c })
	}).Build()
	a := Hydrate(m, Snapshot[string, Context]{})
	defer a.Stop()
	if !errors.Is(a.Err(), ErrAlwaysLimit) {
		t.Fatalf("Err()=%v, want ErrAlwaysLimit", a.Err())
	}
	if err := a.SendCtx(context.Background(), "unused"); !errors.Is(err, ErrActorStopped) {
		t.Fatalf("SendCtx=%v, want ErrActorStopped", err)
	}
	<-cancelled
	if got := a.Data().Count; got != 100 {
		t.Errorf("actions=%d, want 100", got)
	}
}
