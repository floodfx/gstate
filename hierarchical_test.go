package gstate

import (
	"testing"
	"time"
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

	actor := Start(m, Context{})
	
	// Initial state should be [parent, child1]
	// But currently our Start only sets a single state ID.
	// We need to update Start to resolve the initial state stack.
	
	states := actor.States()
	if len(states) != 2 || states[0] != "parent" || states[1] != "child1" {
		t.Errorf("Expected initial state stack [parent, child1], got %v", states)
	}

	// Test transition defined in child
	actor.Send("TO_CHILD2")
	time.Sleep(10 * time.Millisecond)
	states = actor.States()
	if len(states) != 2 || states[0] != "parent" || states[1] != "child2" {
		t.Errorf("Expected state stack [parent, child2], got %v", states)
	}

	// Test transition defined in parent (bubbling up)
	actor.Send("TO_OTHER")
	time.Sleep(10 * time.Millisecond)
	states = actor.States()
	if len(states) != 1 || states[0] != "other" {
		t.Errorf("Expected state stack [other], got %v", states)
	}
}
