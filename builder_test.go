package gstate

import (
	"testing"
)

func TestMachineBuilder(t *testing.T) {
	m := New[StateID, EventID, Context]("test").
		Initial("idle").
		State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("START").GoTo("active")
		}).
		State("active", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("STOP").GoTo("idle")
		}).
		Build()

	if m.ID != "test" {
		t.Errorf("Expected ID 'test', got %s", m.ID)
	}
	if m.Initial != "idle" {
		t.Errorf("Expected initial state 'idle', got %s", m.Initial)
	}
	if len(m.States) != 2 {
		t.Errorf("Expected 2 states, got %d", len(m.States))
	}
}
