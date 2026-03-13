package gstate

import (
	"testing"
	"time"
)

func TestActorPersistence(t *testing.T) {
	m := New[StateID, EventID, Context]("persist").
		Initial("idle").
		State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("START").GoTo("active")
		}).
		State("active", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Entry(func(c Context) Context {
				c.Count++
				return c
			})
			s.On("STOP").GoTo("idle")
		}).
		Build()

	actor := Start(m, Context{Count: 0})
	
	actor.Send("START")
	time.Sleep(10 * time.Millisecond)
	
	if actor.State() != "active" {
		t.Errorf("Expected state active, got %s", actor.State())
	}
	if actor.Snapshot().Context.Count != 1 {
		t.Errorf("Expected count 1, got %d", actor.Snapshot().Context.Count)
	}

	snapshot := actor.Snapshot()
	
	// Create new actor from snapshot
	actor2 := Hydrate(m, snapshot)
	
	if actor2.State() != "active" {
		t.Errorf("Expected hydrated state active, got %s", actor2.State())
	}
	if actor2.Snapshot().Context.Count != 1 {
		t.Errorf("Expected hydrated count 1, got %d", actor2.Snapshot().Context.Count)
	}

	actor2.Send("STOP")
	time.Sleep(10 * time.Millisecond)
	if actor2.State() != "idle" {
		t.Errorf("Expected state idle after hydrated STOP, got %s", actor2.State())
	}
}
