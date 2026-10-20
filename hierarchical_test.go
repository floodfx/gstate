package gstate

import (
	"context"
	"slices"
	"testing"
)

func TestActorHierarchical(t *testing.T) {
	m := New[StateID, EventID, Context]("hierarchical").
		Initial("parent").
		State("parent", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Initial("child1")
			s.On("TO_OTHER").GoTo("other")

			s.State("child1", func(s *StateBuilder[StateID, EventID, Context]) {
				s.On("TO_CHILD2").GoTo("child2")
			})
			s.State("child2", func(s *StateBuilder[StateID, EventID, Context]) {
				s.On("TO_CHILD1").GoTo("child1")
			})
		}).
		State("other", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("BACK").GoTo("parent")
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
	if len(states) != 2 || states[0] != "parent" || states[1] != "child1" {
		t.Errorf("Expected initial state stack [parent, child1], got %v", states)
	}

	// Test transition defined in child
	actor.Send("TO_CHILD2")
	<-step
	states = actor.States()
	if len(states) != 2 || states[0] != "parent" || states[1] != "child2" {
		t.Errorf("Expected state stack [parent, child2], got %v", states)
	}

	// Test transition defined in parent (bubbling up)
	actor.Send("TO_OTHER")
	<-step
	states = actor.States()
	if len(states) != 1 || states[0] != "other" {
		t.Errorf("Expected state stack [other], got %v", states)
	}
}

func TestHierarchicalAncestorAndDescendantTransitions(t *testing.T) {
	m := New[StateID, EventID, Context]("ancestor-descendant").
		Initial("parent").
		State("parent", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Initial("child1")
			s.Entry(func(c Context) Context { c.Count += 10; return c })
			s.Exit(func(c Context) Context { c.Count += 100; return c })
			s.On("JUMP_CHILD2").GoTo("child2")

			s.State("child1", func(s *StateBuilder[StateID, EventID, Context]) {
				s.On("NEXT").GoTo("child2")
			})
			s.State("child2", func(s *StateBuilder[StateID, EventID, Context]) {
				s.On("RESET_PARENT").GoTo("parent")
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

	// Initial entry into parent adds 10.
	if got := actor.Data().Count; got != 10 {
		t.Fatalf("initial Count = %d, want 10", got)
	}
	if got := actor.States(); !slices.Equal(got, []StateID{"parent", "child1"}) {
		t.Fatalf("initial States() = %v, want [parent child1]", got)
	}

	// 1. Parent -> Child transition (parent handles JUMP_CHILD2 -> child2)
	actor.Send("JUMP_CHILD2")
	<-step
	if got := actor.States(); !slices.Equal(got, []StateID{"parent", "child2"}) {
		t.Errorf("after JUMP_CHILD2: States() = %v, want [parent child2]", got)
	}
	// External transition parent -> child2 exits parent (+100) and re-enters parent (+10) -> 120.
	if got := actor.Data().Count; got != 120 {
		t.Errorf("after JUMP_CHILD2: Count = %d, want 120", got)
	}

	// 2. Child -> Parent transition (child2 handles RESET_PARENT -> parent)
	actor.Send("RESET_PARENT")
	<-step
	if got := actor.States(); !slices.Equal(got, []StateID{"parent", "child1"}) {
		t.Errorf("after RESET_PARENT: States() = %v, want [parent child1]", got)
	}
	// External transition child2 -> parent exits parent (+100) and re-enters parent (+10) -> 230.
	if got := actor.Data().Count; got != 230 {
		t.Errorf("after RESET_PARENT: Count = %d, want 230", got)
	}
}
