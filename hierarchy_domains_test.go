package gstate

import (
	"context"
	"slices"
	"testing"
)

func TestExternalHierarchyDomains(t *testing.T) {
	for _, target := range []string{"p", "b"} {
		t.Run(target, func(t *testing.T) {
			m := New[string, string, Context]("external").Initial("p").State("p", func(s *StateBuilder[string, string, Context]) {
				s.Entry(func(c Context) Context { c.Count++; return c })
				s.Exit(func(c Context) Context { c.Count += 10; return c })
				s.Initial("a")
				s.On("GO").GoTo(target)
				s.State("a", func(s *StateBuilder[string, string, Context]) {
					if target == "p" {
						s.On("GO").GoTo(target)
					}
				})
				s.State("b", func(*StateBuilder[string, string, Context]) {})
			}).Build()
			done := make(chan struct{})
			a := Start(m, Context{}, m.WithObservers(ObserverFuncs[string, string, Context]{TransitionFunc: func(context.Context, *TransitionEvent[string, string, Context]) { close(done) }}))
			defer a.Stop()
			a.Send("GO")
			<-done
			if got := a.Data().Count; got != 12 {
				t.Fatalf("entry/exit count=%d, want 12", got)
			}
			leaf := "a"
			if target == "b" {
				leaf = "b"
			}
			if got := a.States(); !slices.Equal(got, []string{"p", leaf}) {
				t.Fatalf("states=%v", got)
			}
		})
	}
}

func TestEntryIntoNestedParallelTarget(t *testing.T) {
	m := New[string, string, Context]("entry").Initial("idle").State("idle", func(s *StateBuilder[string, string, Context]) { s.On("GO").GoTo("b") }).State("p", func(s *StateBuilder[string, string, Context]) {
		s.Type(Parallel)
		s.State("r1", func(s *StateBuilder[string, string, Context]) {
			s.Initial("a")
			s.State("a", func(*StateBuilder[string, string, Context]) {})
		})
		s.State("r2", func(s *StateBuilder[string, string, Context]) {
			s.Initial("b")
			s.State("b", func(*StateBuilder[string, string, Context]) {})
		})
	}).Build()
	done := make(chan struct{})
	a := Start(m, Context{}, m.WithObservers(ObserverFuncs[string, string, Context]{TransitionFunc: func(context.Context, *TransitionEvent[string, string, Context]) { close(done) }}))
	defer a.Stop()
	a.Send("GO")
	<-done
	if got := a.States(); !slices.Equal(got, []string{"p", "r1", "r2", "a", "b"}) {
		t.Fatalf("states=%v", got)
	}
}

func TestParallelRegionExternalReentry(t *testing.T) {
	for _, target := range []string{"r1", "a"} {
		t.Run(target, func(t *testing.T) {
			m := New[string, string, Context]("regions").Initial("p").State("p", func(s *StateBuilder[string, string, Context]) {
				s.Type(Parallel)
				s.State("r1", func(s *StateBuilder[string, string, Context]) {
					s.Initial("a")
					s.Entry(func(c Context) Context { c.Count++; return c })
					s.On("RESET").GoTo(target)
					s.State("a", func(*StateBuilder[string, string, Context]) {})
				})
				s.State("r2", func(s *StateBuilder[string, string, Context]) {
					s.Initial("x")
					s.Entry(func(c Context) Context { c.Count += 100; return c })
					s.State("x", func(s *StateBuilder[string, string, Context]) { s.On("NEXT").GoTo("y") })
					s.State("y", func(*StateBuilder[string, string, Context]) {})
				})
			}).Build()
			done := make(chan struct{}, 2)
			a := Start(m, Context{}, m.WithObservers(ObserverFuncs[string, string, Context]{TransitionFunc: func(context.Context, *TransitionEvent[string, string, Context]) { done <- struct{}{} }}))
			defer a.Stop()
			a.Send("NEXT")
			<-done
			_ = a.States()
			a.Send("RESET")
			<-done
			if got := a.States(); !slices.Equal(got, []string{"p", "r1", "r2", "a", "y"}) {
				t.Errorf("states=%v, sibling must retain y", got)
			}
			if got := a.Data().Count; got != 102 {
				t.Errorf("entry count=%d, want 102", got)
			}
		})
	}
}
