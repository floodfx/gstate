package gstate

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

// TestSortedActiveCacheFlat verifies that States() returns the correct
// active state after every transition in a rapid flat ping/pong loop.
// A stale cache in getSortedActiveStatesLocked would return the previous
// state instead of the current one.
func TestSortedActiveCacheFlat(t *testing.T) {
	type S = string
	type E = string
	type C = struct{}

	m := New[S, E, C]("cache-flat").
		Initial("a").
		State("a", func(s *StateBuilder[S, E, C]) {
			s.On("GO").GoTo("b")
		}).
		State("b", func(s *StateBuilder[S, E, C]) {
			s.On("GO").GoTo("c")
		}).
		State("c", func(s *StateBuilder[S, E, C]) {
			s.On("GO").GoTo("a")
		}).
		Build()

	expected := [][]S{{"b"}, {"c"}, {"a"}, {"b"}, {"c"}, {"a"}}
	results := make([][]S, 0, len(expected))
	var mu sync.Mutex
	done := make(chan struct{})

	obs := ObserverFuncs[S, E, C]{
		TransitionFunc: func(_ context.Context, e TransitionEvent[S, E, C]) {
			// OnTransition fires under the actor lock, so a.active is
			// already updated. We can't call States() here (it takes
			// RLock, we hold the write lock). Record e.To instead.
			mu.Lock()
			results = append(results, []S{e.To})
			if len(results) >= len(expected) {
				select {
				case <-done:
				default:
					close(done)
				}
			}
			mu.Unlock()
		},
	}

	actor := Start(m, struct{}{}, m.WithObserver(obs))
	for i := 0; i < len(expected); i++ {
		actor.Send("GO")
	}
	<-done
	actor.Stop()

	for i, exp := range expected {
		mu.Lock()
		got := results[i]
		mu.Unlock()
		if len(got) != len(exp) || got[0] != exp[0] {
			t.Errorf("transition %d: got %v, want %v", i, got, exp)
		}
	}
}

// TestSortedActiveCacheHierarchical verifies States() correctness across
// transitions that change the active-state set at multiple depths.
// Enter a compound state (parent + child), then transition to a sibling
// compound state (different parent + child). The cache must reflect the
// full exit/entry chain.
func TestSortedActiveCacheHierarchical(t *testing.T) {
	type S = string
	type E = string
	type C = struct{}

	m := New[S, E, C]("cache-hier").
		Initial("left").
		State("left", func(s *StateBuilder[S, E, C]) {
			s.Initial("left.child")
			s.State("left.child", func(cs *StateBuilder[S, E, C]) {
				cs.On("SWAP").GoTo("right")
			})
		}).
		State("right", func(s *StateBuilder[S, E, C]) {
			s.Initial("right.child")
			s.State("right.child", func(cs *StateBuilder[S, E, C]) {
				cs.On("SWAP").GoTo("left")
			})
		}).
		Build()

	type stateSnap struct {
		from, to S
	}
	var mu sync.Mutex
	var transitions []stateSnap
	done := make(chan struct{})
	const rounds = 6

	obs := ObserverFuncs[S, E, C]{
		TransitionFunc: func(_ context.Context, e TransitionEvent[S, E, C]) {
			mu.Lock()
			transitions = append(transitions, stateSnap{from: e.From, to: e.To})
			if len(transitions) >= rounds {
				select {
				case <-done:
				default:
					close(done)
				}
			}
			mu.Unlock()
		},
	}

	actor := Start(m, struct{}{}, m.WithObserver(obs))

	// Initial: [left, left.child]
	states := actor.States()
	if len(states) != 2 || states[0] != "left" || states[1] != "left.child" {
		t.Fatalf("initial states: got %v, want [left, left.child]", states)
	}

	for i := 0; i < rounds; i++ {
		actor.Send("SWAP")
	}
	<-done
	actor.Stop()

	// Verify alternating pattern.
	expectedTransitions := []stateSnap{
		{"left.child", "right"},
		{"right.child", "left"},
		{"left.child", "right"},
		{"right.child", "left"},
		{"left.child", "right"},
		{"right.child", "left"},
	}
	mu.Lock()
	defer mu.Unlock()
	for i, exp := range expectedTransitions {
		if i >= len(transitions) {
			t.Fatalf("missing transition %d", i)
		}
		if transitions[i] != exp {
			t.Errorf("transition %d: got %v, want %v", i, transitions[i], exp)
		}
	}
}

// TestSortedActiveCacheParallel verifies States() after entering and
// exiting a parallel state with multiple regions. The cache must
// correctly reflect all region activations and deactivations.
func TestSortedActiveCacheParallel(t *testing.T) {
	type S = string
	type E = string
	type C = struct{}

	m := New[S, E, C]("cache-par").
		Initial("idle").
		State("idle", func(s *StateBuilder[S, E, C]) {
			s.On("GO").GoTo("par")
		}).
		State("par", func(s *StateBuilder[S, E, C]) {
			s.Type(Parallel)
			s.On("BACK").GoTo("idle")
			for r := 0; r < 3; r++ {
				region := S(fmt.Sprintf("r%d", r))
				child := S(fmt.Sprintf("r%d.a", r))
				s.State(region, func(rs *StateBuilder[S, E, C]) {
					rs.Initial(child)
					rs.State(child, func(_ *StateBuilder[S, E, C]) {})
				})
			}
		}).
		Build()

	enteredPar := make(chan struct{})
	backToIdle := make(chan struct{})
	count := 0

	obs := ObserverFuncs[S, E, C]{
		TransitionFunc: func(_ context.Context, e TransitionEvent[S, E, C]) {
			count++
			if count == 1 {
				close(enteredPar)
			}
			if count == 2 {
				close(backToIdle)
			}
		},
	}

	actor := Start(m, struct{}{}, m.WithObserver(obs))

	// idle -> par
	actor.Send("GO")
	<-enteredPar

	states := actor.States()
	// Should have: par, r0, r0.a, r1, r1.a, r2, r2.a = 7 states
	if len(states) != 7 {
		t.Errorf("after GO: got %d states %v, want 7", len(states), states)
	}
	// Root should be "par"
	if states[0] != "par" {
		t.Errorf("after GO: root state = %q, want \"par\"", states[0])
	}

	// par -> idle
	actor.Send("BACK")
	<-backToIdle

	states = actor.States()
	if len(states) != 1 || states[0] != "idle" {
		t.Errorf("after BACK: got %v, want [idle]", states)
	}

	actor.Stop()
}

// TestSortedActiveCacheAlwaysChain verifies that the cache stays correct
// through a chain of always-transitions where the active set mutates
// multiple times within a single event dispatch.
func TestSortedActiveCacheAlwaysChain(t *testing.T) {
	type S = string
	type E = string
	type C = struct{}

	// s0 --always--> s1 --always--> s2 --always--> terminal
	m := New[S, E, C]("cache-always").
		Initial("start").
		State("start", func(s *StateBuilder[S, E, C]) {
			s.On("GO").GoTo("s0")
		}).
		State("s0", func(s *StateBuilder[S, E, C]) {
			s.Always().GoTo("s1")
		}).
		State("s1", func(s *StateBuilder[S, E, C]) {
			s.Always().GoTo("s2")
		}).
		State("s2", func(s *StateBuilder[S, E, C]) {
			s.Always().GoTo("terminal")
		}).
		State("terminal", func(s *StateBuilder[S, E, C]) {
			s.On("RESET").GoTo("start")
		}).
		Build()

	done := make(chan struct{})
	var mu sync.Mutex
	var transitions []string

	obs := ObserverFuncs[S, E, C]{
		TransitionFunc: func(_ context.Context, e TransitionEvent[S, E, C]) {
			mu.Lock()
			transitions = append(transitions, e.To)
			// GO produces: s0, s1, s2, terminal (4 transitions)
			// RESET produces: start (1 transition)
			// Total = 5
			if len(transitions) >= 5 {
				select {
				case <-done:
				default:
					close(done)
				}
			}
			mu.Unlock()
		},
	}

	actor := Start(m, struct{}{}, m.WithObserver(obs))
	actor.Send("GO")
	actor.Send("RESET")
	<-done

	// After the always chain, we should be in "start" (from RESET).
	state := actor.State()
	if state != "start" {
		t.Errorf("final state: got %q, want \"start\"", state)
	}

	mu.Lock()
	expected := []string{"s0", "s1", "s2", "terminal", "start"}
	for i, exp := range expected {
		if i >= len(transitions) {
			t.Fatalf("missing transition %d", i)
		}
		if transitions[i] != exp {
			t.Errorf("transition %d: got %q, want %q", i, transitions[i], exp)
		}
	}
	mu.Unlock()

	actor.Stop()
}
