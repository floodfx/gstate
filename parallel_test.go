package gstate

import (
	"context"
	"slices"
	"testing"
)

func TestActorParallel(t *testing.T) {
	m := New[StateID, EventID, Context]("parallel").
		Initial("parent").
		State("parent", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Type(Parallel)
			s.State("region1", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Initial("s1")
				s.State("s1", func(s *StateBuilder[StateID, EventID, Context]) {
					s.On("TO_S2").GoTo("s2")
				})
				s.State("s2", func(s *StateBuilder[StateID, EventID, Context]) {
					s.On("TO_S1").GoTo("s1")
				})
			})
			s.State("region2", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Initial("s3")
				s.State("s3", func(s *StateBuilder[StateID, EventID, Context]) {
					s.On("TO_S4").GoTo("s4")
				})
				s.State("s4", func(s *StateBuilder[StateID, EventID, Context]) {
					s.On("TO_S3").GoTo("s3")
				})
			})
		}).
		Build()

	step := make(chan struct{})
	obs := ObserverFuncs[StateID, EventID, Context]{
		TransitionFunc: func(context.Context, *TransitionEvent[StateID, EventID, Context]) {
			step <- struct{}{}
		},
	}
	actor := Start(m, Context{}, m.WithObservers(obs))
	defer actor.Stop()

	states := actor.States()
	if !slices.Contains(states, "s1") || !slices.Contains(states, "s3") {
		t.Errorf("Expected both s1 and s3 to be active, got %v", states)
	}

	// Transition in region1
	actor.Send("TO_S2")
	<-step
	states = actor.States()
	if !slices.Contains(states, "s2") || !slices.Contains(states, "s3") {
		t.Errorf("Expected both s2 and s3 to be active, got %v", states)
	}

	// Transition in region2
	actor.Send("TO_S4")
	<-step
	states = actor.States()
	if !slices.Contains(states, "s2") || !slices.Contains(states, "s4") {
		t.Errorf("Expected both s2 and s4 to be active, got %v", states)
	}
}

func TestParallelBroadcastAndConflictResolution(t *testing.T) {
	t.Run("broadcast_internal_and_external_across_regions_with_always_after", func(t *testing.T) {
		done := make(chan struct{})
		rec := &RecordingObserver[StateID, EventID, Context]{}

		m := New[StateID, EventID, Context]("broadcast").
			Initial("p").
			State("p", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Type(Parallel)
				s.State("r1", func(s *StateBuilder[StateID, EventID, Context]) {
					s.Initial("r1a")
					s.State("r1a", func(s *StateBuilder[StateID, EventID, Context]) {
						s.On("GO").Assign(func(c Context) Context { c.Count += 1; return c }).GoTo("r1b")
					})
					s.State("r1b", func(s *StateBuilder[StateID, EventID, Context]) {
						// Always transition requires both regions' GO actions to have completed first (Count == 11)
						s.Always().
							Guard(func(c Context) bool { return c.Count == 11 }).
							Assign(func(c Context) Context { c.Count += 100; return c }).
							GoTo("r1c")
					})
					s.State("r1c", func(_ *StateBuilder[StateID, EventID, Context]) {})
				})
				s.State("r2", func(s *StateBuilder[StateID, EventID, Context]) {
					s.Initial("r2a")
					s.State("r2a", func(s *StateBuilder[StateID, EventID, Context]) {
						s.On("GO").Assign(func(c Context) Context { c.Count += 10; return c })
					})
				})
			}).
			Build()

		obs := ObserverFuncs[StateID, EventID, Context]{
			TransitionFunc: func(_ context.Context, e *TransitionEvent[StateID, EventID, Context]) {
				if e.To == "r1c" {
					close(done)
				}
			},
		}
		a := Start(m, Context{}, m.WithObservers(rec, obs))
		defer a.Stop()

		a.Send("GO")
		<-done

		if got := a.Data().Count; got != 111 {
			t.Errorf("Data().Count = %d, want 111 (both regions fired GO before Always evaluated)", got)
		}
		wantStates := []StateID{"p", "r1", "r2", "r1c", "r2a"}
		if got := a.States(); !slices.Equal(got, wantStates) {
			t.Errorf("States() = %v, want %v", got, wantStates)
		}
		if dropped := rec.EventsDropped(); len(dropped) != 0 {
			t.Errorf("unexpected dropped events: %+v", dropped)
		}
	})

	t.Run("child_suppresses_ancestor_and_unrelated_region_stays_active", func(t *testing.T) {
		done := make(chan struct{})
		count := 0
		m := New[StateID, EventID, Context]("conflict").
			Initial("p").
			State("p", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Type(Parallel)
				// Ancestor transition on p would exit p, but r1a handles EV so r1a wins and preempts p!
				s.On("EV").Assign(func(c Context) Context { c.Count += 1000; return c }).GoTo("outside")
				s.State("r1", func(s *StateBuilder[StateID, EventID, Context]) {
					s.Initial("r1a")
					s.On("EV").Assign(func(c Context) Context { c.Count += 100; return c }).GoTo("r1b")
					s.State("r1a", func(s *StateBuilder[StateID, EventID, Context]) {
						s.On("EV").Assign(func(c Context) Context { c.Count += 1; return c }).GoTo("r1b")
					})
					s.State("r1b", func(_ *StateBuilder[StateID, EventID, Context]) {})
				})
				s.State("r2", func(s *StateBuilder[StateID, EventID, Context]) {
					s.Initial("r2a")
					s.State("r2a", func(s *StateBuilder[StateID, EventID, Context]) {
						s.On("EV").Assign(func(c Context) Context { c.Count += 10; return c }).GoTo("r2b")
					})
					s.State("r2b", func(_ *StateBuilder[StateID, EventID, Context]) {})
				})
				s.State("r3", func(s *StateBuilder[StateID, EventID, Context]) {
					s.Initial("r3a")
					s.State("r3a", func(_ *StateBuilder[StateID, EventID, Context]) {})
				})
			}).
			State("outside", func(_ *StateBuilder[StateID, EventID, Context]) {}).
			Build()

		obs := ObserverFuncs[StateID, EventID, Context]{
			TransitionFunc: func(context.Context, *TransitionEvent[StateID, EventID, Context]) {
				count++
				if count == 2 {
					close(done)
				}
			},
		}
		a := Start(m, Context{}, m.WithObservers(obs))
		defer a.Stop()

		a.Send("EV")
		<-done

		if got := a.Data().Count; got != 11 {
			t.Errorf("Data().Count = %d, want 11 (r1a +1 and r2a +10; r1 and p suppressed)", got)
		}
		wantStates := []StateID{"p", "r1", "r2", "r3", "r1b", "r2b", "r3a"}
		if got := a.States(); !slices.Equal(got, wantStates) {
			t.Errorf("States() = %v, want %v", got, wantStates)
		}
	})

	t.Run("conflicting_transitions_resolved_deterministically", func(t *testing.T) {
		done := make(chan struct{})
		m := New[StateID, EventID, Context]("conflict-exit").
			Initial("p").
			State("p", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Type(Parallel)
				s.State("r1", func(s *StateBuilder[StateID, EventID, Context]) {
					s.Initial("r1a")
					s.State("r1a", func(s *StateBuilder[StateID, EventID, Context]) {
						// Exits the entire parallel state p
						s.On("ESCAPE").Assign(func(c Context) Context { c.Count += 1; return c }).GoTo("outside")
					})
				})
				s.State("r2", func(s *StateBuilder[StateID, EventID, Context]) {
					s.Initial("r2a")
					s.State("r2a", func(s *StateBuilder[StateID, EventID, Context]) {
						// Conflicting transition inside r2
						s.On("ESCAPE").Assign(func(c Context) Context { c.Count += 10; return c }).GoTo("r2b")
					})
					s.State("r2b", func(_ *StateBuilder[StateID, EventID, Context]) {})
				})
			}).
			State("outside", func(_ *StateBuilder[StateID, EventID, Context]) {}).
			Build()

		obs := ObserverFuncs[StateID, EventID, Context]{
			TransitionFunc: func(context.Context, *TransitionEvent[StateID, EventID, Context]) {
				close(done)
			},
		}
		a := Start(m, Context{}, m.WithObservers(obs))
		defer a.Stop()

		a.Send("ESCAPE")
		<-done

		if got := a.Data().Count; got != 1 {
			t.Errorf("Data().Count = %d, want 1 (r1a selected deterministically, r2a suppressed due to exit-set conflict)", got)
		}
		wantStates := []StateID{"outside"}
		if got := a.States(); !slices.Equal(got, wantStates) {
			t.Errorf("States() = %v, want %v", got, wantStates)
		}
	})

	t.Run("outside_to_nested_region_activates_all_sibling_regions", func(t *testing.T) {
		done := make(chan struct{})
		m := New[StateID, EventID, Context]("outside-to-nested").
			Initial("idle").
			State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
				s.On("DEEP_ENTER").GoTo("r1b")
			}).
			State("p", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Type(Parallel)
				s.State("r1", func(s *StateBuilder[StateID, EventID, Context]) {
					s.Initial("r1a")
					s.State("r1a", func(_ *StateBuilder[StateID, EventID, Context]) {})
					s.State("r1b", func(_ *StateBuilder[StateID, EventID, Context]) {})
				})
				s.State("r2", func(s *StateBuilder[StateID, EventID, Context]) {
					s.Initial("r2a")
					s.State("r2a", func(_ *StateBuilder[StateID, EventID, Context]) {})
				})
			}).
			Build()

		obs := ObserverFuncs[StateID, EventID, Context]{
			TransitionFunc: func(context.Context, *TransitionEvent[StateID, EventID, Context]) {
				close(done)
			},
		}
		a := Start(m, Context{}, m.WithObservers(obs))
		defer a.Stop()

		a.Send("DEEP_ENTER")
		<-done

		wantStates := []StateID{"p", "r1", "r2", "r1b", "r2a"}
		if got := a.States(); !slices.Equal(got, wantStates) {
			t.Errorf("States() = %v, want %v", got, wantStates)
		}
	})
}
