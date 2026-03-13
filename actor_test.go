package gstate

import (
	"testing"
	"time"
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

	actor := Start(m, Context{Count: 0})
	
	if actor.State() != "idle" {
		t.Errorf("Expected initial state 'idle', got %s", actor.State())
	}

	actor.Send("START")
	time.Sleep(10 * time.Millisecond)
	if actor.State() != "active" {
		t.Errorf("Expected state 'active' after START event, got %s", actor.State())
	}

	actor.Send("STOP")
	time.Sleep(10 * time.Millisecond)
	if actor.State() != "idle" {
		t.Errorf("Expected state 'idle' after STOP event, got %s", actor.State())
	}
}
