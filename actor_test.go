package gstate

import (
	"context"
	"testing"
)

func TestActorBasic(t *testing.T) {
	m := New[StateID, EventID, Context]("test").
		Initial("idle").
		State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("START").GoTo("active")
		}).
		State("active", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("STOP").GoTo("idle")
		}).
		Build()

	step := make(chan struct{}, 1)
	actor := Start(m, Context{}, m.WithObservers(ObserverFuncs[StateID, EventID, Context]{TransitionFunc: func(context.Context, *TransitionEvent[StateID, EventID, Context]) { step <- struct{}{} }}))
	defer actor.Stop()

	if actor.State() != "idle" {
		t.Errorf("Expected initial state 'idle', got %s", actor.State())
	}

	actor.Send("START")
	<-step
	if actor.State() != "active" {
		t.Errorf("Expected state 'active' after START event, got %s", actor.State())
	}

	actor.Send("STOP")
	<-step
	if actor.State() != "idle" {
		t.Errorf("Expected state 'idle' after STOP event, got %s", actor.State())
	}
}
