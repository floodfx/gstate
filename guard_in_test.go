package gstate

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

type guardInState = StateBuilder[StateID, EventID, Context]

func TestGuardIn_OnDoneRoutesOnCompletedFinal(t *testing.T) {
	build := func(initial StateID) *Machine[StateID, EventID, Context] {
		return New[StateID, EventID, Context]("review").
			Initial("review").
			State("review", func(s *guardInState) {
				s.Initial(initial)
				s.State("approved", func(f *guardInState) { f.Type(Final) })
				s.State("revise", func(f *guardInState) { f.Type(Final) })
				s.State("rejected", func(f *guardInState) { f.Type(Final) })
				s.OnDone().GuardIn("approved").GoTo("ship")
				s.OnDone().GuardIn("revise").GoTo("fix")
				s.OnDone().GoTo("failed")
			}).
			State("ship", func(*guardInState) {}).
			State("fix", func(*guardInState) {}).
			State("failed", func(*guardInState) {}).
			Build()
	}
	for _, tc := range []struct{ final, want StateID }{
		{"approved", "ship"},
		{"revise", "fix"},
		{"rejected", "failed"},
	} {
		t.Run(string(tc.final), func(t *testing.T) {
			a := Start(build(tc.final), Context{})
			defer a.Stop()
			if got := a.State(); got != tc.want {
				t.Fatalf("State() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGuardIn_AlwaysSeesOtherParallelRegion(t *testing.T) {
	m := New[StateID, EventID, Context]("always").
		Initial("p").
		State("p", func(p *guardInState) {
			p.Type(Parallel)
			p.State("r1", func(r *guardInState) {
				r.Initial("a1")
				r.State("a1", func(s *guardInState) {
					s.Always().GuardIn("b2").GoTo("a3")
					s.Always().GuardIn("b1").GoTo("a2")
				})
				r.State("a2", func(*guardInState) {})
				r.State("a3", func(*guardInState) {})
			})
			p.State("r2", func(r *guardInState) {
				r.Initial("b1")
				r.State("b1", func(*guardInState) {})
				r.State("b2", func(*guardInState) {})
			})
		}).
		Build()
	a := Start(m, Context{})
	defer a.Stop()
	if !a.Matches("a2") || a.Matches("a1") || a.Matches("a3") {
		t.Fatalf("active = %v, want a2 (not a1 or a3)", a.States())
	}
}

func TestGuardIn_ANDsWithFunctionGuard(t *testing.T) {
	var calls []string
	m := New[StateID, EventID, Context]("and").
		Initial("work").
		State("work", func(s *guardInState) {
			s.Initial("x")
			s.State("x", func(*guardInState) {})
			s.State("y", func(*guardInState) {})
			s.Always().GuardIn("y").
				GuardErr(func(Context) (bool, error) {
					calls = append(calls, "first")
					return false, errors.New("boom")
				}).
				OnGuardErr("failed", nil).
				GoTo("bad")
			s.Always().GuardIn("x").
				Guard(func(Context) bool {
					calls = append(calls, "second")
					return false
				}).
				GoTo("bad")
			s.Always().GuardIn("x").
				Guard(func(Context) bool {
					calls = append(calls, "third")
					return true
				}).
				GoTo("ok")
		}).
		State("ok", func(*guardInState) {}).
		State("bad", func(*guardInState) {}).
		State("failed", func(*guardInState) {}).
		Build()
	a := Start(m, Context{})
	defer a.Stop()
	if got := a.State(); got != "ok" {
		t.Fatalf("State() = %q, want ok", got)
	}
	if !slices.Equal(calls, []string{"second", "third"}) {
		t.Fatalf("guard calls = %v, want [second third]", calls)
	}
}

func TestGuardIn_Exports(t *testing.T) {
	m := New[StateID, EventID, Context]("exports").
		Initial("a").
		State("a", func(s *guardInState) {
			s.On("GO").GuardIn("b").GoTo("b")
			s.On("NEXT").GuardIn("b").Guard(func(Context) bool { return true }).GuardLabel("ready").GoTo("c")
		}).
		State("b", func(*guardInState) {}).
		State("c", func(*guardInState) {}).
		Build()

	mermaid := ToMermaid(m)
	for _, want := range []string{`a -->|"GO [in b]"| b`, `a -->|"NEXT [in b, ready]"| c`} {
		if !strings.Contains(mermaid, want) {
			t.Errorf("mermaid missing %q:\n%s", want, mermaid)
		}
	}

	scxml, err := ToSCXMLString(m)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`cond="In(&#39;b&#39;)"`, `cond="In(&#39;b&#39;) and ready"`} {
		if !strings.Contains(scxml, want) {
			t.Errorf("scxml missing %s:\n%s", want, scxml)
		}
	}
}
