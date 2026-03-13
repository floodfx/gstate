package gstate

import (
	"testing"
	"time"
)

func TestActorHistory(t *testing.T) {
	m := New[StateID, EventID, Context]("history").
		Initial("parent").
		State("parent", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Initial("s1")
			s.History(Shallow)
			s.State("s1", func(s *StateBuilder[StateID, EventID, Context]) {
				s.On("TO_S2").GoTo("s2")
			})
			s.State("s2", func(s *StateBuilder[StateID, EventID, Context]) {
				s.On("TO_S1").GoTo("s1")
			})
			s.On("GO_AWAY").GoTo("other")
		}).
		State("other", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("BACK").GoTo("parent")
		}).
		Build()

	actor := Start(m, Context{})
	
	// Initial state is s1
	if actor.State() != "s1" {
		t.Errorf("Expected initial state s1, got %s", actor.State())
	}

	// Move to s2
	actor.Send("TO_S2")
	time.Sleep(10 * time.Millisecond)
	if actor.State() != "s2" {
		t.Errorf("Expected state s2, got %s", actor.State())
	}

	// Move out of parent
	actor.Send("GO_AWAY")
	time.Sleep(10 * time.Millisecond)
	if actor.State() != "other" {
		t.Errorf("Expected state other, got %s", actor.State())
	}

	// Move back to parent - should restore s2 because of history
	actor.Send("BACK")
	time.Sleep(10 * time.Millisecond)
	if actor.State() != "s2" {
		t.Errorf("Expected restored state s2, got %s", actor.State())
	}
}
