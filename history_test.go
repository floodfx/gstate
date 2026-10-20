package gstate

import (
	"context"
	"testing"
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

	step := make(chan struct{}, 1)
	actor := Start(m, Context{}, m.WithObservers(ObserverFuncs[StateID, EventID, Context]{TransitionFunc: func(context.Context, *TransitionEvent[StateID, EventID, Context]) { step <- struct{}{} }}))
	defer actor.Stop()

	// Initial state is s1
	if actor.State() != "s1" {
		t.Errorf("Expected initial state s1, got %s", actor.State())
	}

	// Move to s2
	actor.Send("TO_S2")
	<-step
	if actor.State() != "s2" {
		t.Errorf("Expected state s2, got %s", actor.State())
	}

	// Move out of parent
	actor.Send("GO_AWAY")
	<-step
	if actor.State() != "other" {
		t.Errorf("Expected state other, got %s", actor.State())
	}

	// Move back to parent - should restore s2 because of history
	actor.Send("BACK")
	<-step
	if actor.State() != "s2" {
		t.Errorf("Expected restored state s2, got %s", actor.State())
	}
}
