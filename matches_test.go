package gstate

import "testing"

func TestMatchesActiveHierarchy(t *testing.T) {
	m := New[StateID, EventID, Context]("matches").Initial("parent").State("parent", func(s *StateBuilder[StateID, EventID, Context]) {
		s.Type(Parallel)
		s.State("left", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Initial("leaf")
			s.State("leaf", func(s *StateBuilder[StateID, EventID, Context]) {})
			s.State("inactive", func(s *StateBuilder[StateID, EventID, Context]) {})
		})
		s.State("right", func(s *StateBuilder[StateID, EventID, Context]) {})
	}).Build()
	a := Start(m, Context{})
	defer a.Stop()
	for _, state := range []StateID{"parent", "left", "leaf", "right"} {
		if !a.Matches(state) {
			t.Errorf("active state %q did not match", state)
		}
	}
	for _, state := range []StateID{"inactive", "unknown", ""} {
		if a.Matches(state) {
			t.Errorf("inactive state %q matched", state)
		}
	}
	a.Stop()
	if !a.Matches("leaf") {
		t.Fatal("shutdown lost the final configuration")
	}
}
