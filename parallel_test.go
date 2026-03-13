package gstate

import (
	"testing"
	"time"
)

func TestActorParallel(t *testing.T) {
	m := New[StateID, EventID, Context]("parallel").
		Initial("parent").
		State("parent", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Type(Parallel)
			s.State("region1", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Initial("s1")
				s.State("s1", func(s *StateBuilder[StateID, EventID, Context]) {
					s.On("TO_S2").GoTo("s2")
				})
				s.State("s2", func(s *StateBuilder[StateID, EventID, Context]) {
					s.On("TO_S1").GoTo("s1")
				})
			})
			s.State("region2", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Initial("s3")
				s.State("s3", func(s *StateBuilder[StateID, EventID, Context]) {
					s.On("TO_S4").GoTo("s4")
				})
				s.State("s4", func(s *StateBuilder[StateID, EventID, Context]) {
					s.On("TO_S3").GoTo("s3")
				})
			})
		}).
		Build()

	actor := Start(m, Context{})
	
	// Should be in {s1, s3}
	states := actor.States()
	// How should we return multiple stacks?
	// The spec says "a stack of active states (e.g., []S{parent, child})".
	// For parallel, it could be [parent, region1, s1, region2, s3]? 
	// Or maybe actor.States() returns ALL active leaf states?
	// Let's assume for now it returns all active states.
	
	hasS1 := false
	hasS3 := false
	for _, sID := range states {
		if sID == "s1" { hasS1 = true }
		if sID == "s3" { hasS3 = true }
	}
	if !hasS1 || !hasS3 {
		t.Errorf("Expected both s1 and s3 to be active, got %v", states)
	}

	// Transition in region1
	actor.Send("TO_S2")
	time.Sleep(10 * time.Millisecond)
	states = actor.States()
	hasS2 := false
	hasS3 = false
	for _, sID := range states {
		if sID == "s2" { hasS2 = true }
		if sID == "s3" { hasS3 = true }
	}
	if !hasS2 || !hasS3 {
		t.Errorf("Expected both s2 and s3 to be active, got %v", states)
	}

	// Transition in region2
	actor.Send("TO_S4")
	time.Sleep(10 * time.Millisecond)
	states = actor.States()
	hasS2 = false
	hasS4 := false
	for _, sID := range states {
		if sID == "s2" { hasS2 = true }
		if sID == "s4" { hasS4 = true }
	}
	if !hasS2 || !hasS4 {
		t.Errorf("Expected both s2 and s4 to be active, got %v", states)
	}
}
