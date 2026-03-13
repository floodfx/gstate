package gstate

import (
	"testing"
	"time"
)

func TestActorAfter(t *testing.T) {
	m := New[StateID, EventID, Context]("after").
		Initial("idle").
		State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
			s.After(20 * time.Millisecond).GoTo("timeout")
		}).
		State("timeout", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Type(Final)
		}).
		Build()

	actor := Start(m, Context{})
	
	if actor.State() != "idle" {
		t.Errorf("Expected state idle, got %s", actor.State())
	}

	time.Sleep(50 * time.Millisecond)
	if actor.State() != "timeout" {
		t.Errorf("Expected state timeout after delay, got %s", actor.State())
	}
}
