package gstate

import (
	"context"
	"strings"
	"testing"
)

type compositionBuilder = MachineBuilder[StateID, EventID, Context]
type compositionState = StateBuilder[StateID, EventID, Context]

func completionBuilder(configure func(*compositionState)) *compositionBuilder {
	return New[StateID, EventID, Context]("completion").Initial("work").
		State("work", func(s *compositionState) {
			s.Initial("ready")
			s.State("ready", func(f *compositionState) { f.Type(Final) })
			configure(s)
		}).State("next", func(*compositionState) {})
}

func TestCompletionBuilderValidation(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*compositionState)
		want      string
	}{
		{"valid", func(s *compositionState) {
			s.OnDone().Guard(func(Context) bool { return false }).GoTo("next")
			s.OnDone().GoTo("next")
		}, ""},
		{"targetless", func(s *compositionState) { s.OnDone() }, "target"},
		{"self", func(s *compositionState) { s.OnDone().GoTo("work") }, "subtree"},
		{"descendant", func(s *compositionState) { s.OnDone().GoTo("ready") }, "subtree"},
		{"unknown", func(s *compositionState) { s.OnDone().GoTo("missing") }, "does not exist"},
		{"guarded fallback", func(s *compositionState) { s.OnDone().Guard(func(Context) bool { return true }).GoTo("next") }, "unconditional"},
		{"candidate after fallback", func(s *compositionState) {
			s.OnDone().GoTo("next")
			s.OnDone().Guard(func(Context) bool { return false }).GoTo("next")
		}, "fallback"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := completionBuilder(tt.configure).Validate()
			if tt.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("want %q error, got %v", tt.want, err)
			}
		})
	}
}

func TestCompletionRejectsImpossibleKinds(t *testing.T) {
	for _, kind := range []StateType{Atomic, Final, Compound, Parallel} {
		t.Run(map[StateType]string{Atomic: "atomic", Final: "final", Compound: "compound", Parallel: "parallel"}[kind], func(t *testing.T) {
			b := New[StateID, EventID, Context]("invalid").Initial("work").State("work", func(s *compositionState) {
				s.Type(kind)
				if kind == Compound || kind == Parallel {
					if kind == Compound {
						s.Initial("child")
					}
					s.State("child", func(*compositionState) {})
				}
				s.OnDone().GoTo("next")
			}).State("next", func(*compositionState) {})
			if err := b.Validate(); err == nil {
				t.Fatal("accepted structurally impossible completion")
			}
		})
	}
}

func TestCompletionAllowsAncestorTarget(t *testing.T) {
	b := New[StateID, EventID, Context]("ancestor").Initial("root").State("root", func(r *compositionState) {
		r.Initial("work")
		r.State("work", func(s *compositionState) {
			s.Initial("ready")
			s.State("ready", func(f *compositionState) { f.Type(Final) })
			s.OnDone().GoTo("root")
		})
	})
	if err := b.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestExclusiveBuilderWrites(t *testing.T) {
	service := func(context.Context, Context, func(func(Context) Context)) error { return nil }
	tests := []struct {
		name      string
		configure func(*compositionState)
		want      string
	}{
		{"initial", func(s *compositionState) { s.Initial("ready") }, "initial"},
		{"wrong replacement", func(s *compositionState) { s.ReplaceInitial("wrong", "ready") }, "initial"},
		{"invoke", func(s *compositionState) { s.Invoke(service, "", ""); s.Invoke(service, "", "") }, "invoke"},
		{"wrong invoke replacement", func(s *compositionState) {
			s.Invoke(service, "", "")
			s.InvokeLabel("original")
			s.ReplaceInvoke("wrong", service, "", "")
		}, "invoke"},
		{"unlabeled invoke replacement", func(s *compositionState) { s.Invoke(service, "", ""); s.ReplaceInvoke("", service, "", "") }, "invoke"},
		{"explicit atomic", func(s *compositionState) { s.Type(Atomic); s.Type(Compound) }, "type"},
		{"history none", func(s *compositionState) { s.History(None); s.History(Deep) }, "history"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := completionBuilder(tt.configure)
			err := b.Validate()
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), tt.want) {
				t.Fatalf("want %q error, got %v", tt.want, err)
			}
			if m, err := b.BuildWithError(); m != nil || err == nil {
				t.Fatal("BuildWithError lost diagnostics")
			}
		})
	}
}

func TestExplicitBuilderReplacementsAndInferredType(t *testing.T) {
	service := func(context.Context, Context, func(func(Context) Context)) error { return nil }
	b := completionBuilder(func(s *compositionState) {
		s.ReplaceInitial("ready", "other")
		s.State("other", func(f *compositionState) { f.Type(Final) })
		s.Type(Compound)
		s.Type(Compound)
		s.History(None)
		s.History(None)
		s.Invoke(service, "", "")
		s.InvokeLabel("old")
		s.ReplaceInvoke("old", service, "next", "")
		s.InvokeLabel("new")
	})
	m, err := b.BuildWithError()
	if err != nil {
		t.Fatal(err)
	}
	if m.States["work"].Initial != "other" || m.States["work"].Invoke.OnDone != "next" {
		t.Fatal("replacement not applied")
	}
}

func TestBuilderActionLabelMisuse(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*compositionState)
	}{
		{"entry before callback", func(s *compositionState) { s.EntryLabel("bad") }},
		{"exit before callback", func(s *compositionState) { s.ExitLabel("bad") }},
		{"empty entry", func(s *compositionState) { s.Entry(func(d Context) Context { return d }); s.EntryLabel("") }},
		{"duplicate exit", func(s *compositionState) {
			s.Exit(func(d Context) Context { return d })
			s.ExitLabel("first")
			s.ExitLabel("second")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := completionBuilder(tt.configure).Validate(); err == nil {
				t.Fatal("accepted invalid action label")
			}
		})
	}
}

func TestBuilderConflictsThroughUseAndExtend(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(map[bool]string{false: "forward", true: "reverse"}[reverse], func(t *testing.T) {
			b := completionBuilder(func(s *compositionState) { s.State("alternate", func(f *compositionState) { f.Type(Final) }) })
			first := func(m *compositionBuilder) {
				m.Extend("work", func(s *compositionState) { s.ReplaceInitial("ready", "alternate") })
			}
			// Both plugins change their expected default to catch either order.
			second := func(m *compositionBuilder) {
				m.Extend("work", func(s *compositionState) {
					s.ReplaceInitial("ready", "other")
					s.State("other", func(f *compositionState) { f.Type(Final) })
				})
			}
			if reverse {
				first, second = second, first
			}
			b.Use(first, second)
			if err := b.Validate(); err == nil {
				t.Fatal("conflicting replacements accepted")
			}
			want := StateID("alternate")
			if reverse {
				want = "other"
			}
			if b.machine.States["work"].Initial != want {
				t.Fatal("invalid replacement overwrote previous valid configuration")
			}
		})
	}
}

func TestBuilderErrorsPersistAndBuildPanics(t *testing.T) {
	b := completionBuilder(func(s *compositionState) { s.Initial("bad"); s.ReplaceInitial("ready", "ready") })
	if got := b.machine.States["work"].Initial; got != "ready" {
		t.Fatalf("failed assignment changed initial to %q", got)
	}
	if b.Validate() == nil {
		t.Fatal("subsequent valid write erased diagnostic")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("Build did not panic on recorded conflict")
		}
	}()
	b.Build()
}

func TestBuilderLabelsArePerAction(t *testing.T) {
	identity := func(d Context) Context { return d }
	b := completionBuilder(func(s *compositionState) {
		s.EntryNamed("first", identity)
		s.Entry(identity)
		s.EntryLabel("second")
		s.Entry(identity)
		s.ExitNamed("last", identity)
		s.Exit(identity)
	})
	if err := b.Validate(); err != nil {
		t.Fatal(err)
	}
	sd := b.machine.States["work"]
	if len(sd.entryLabels) != 3 || sd.entryLabels[0] != "first" || sd.entryLabels[1] != "second" || sd.entryLabels[2] != "" {
		t.Fatalf("entry labels: %q", sd.entryLabels)
	}
	if len(sd.exitLabels) != 2 || sd.exitLabels[0] != "last" || sd.exitLabels[1] != "" {
		t.Fatalf("exit labels: %q", sd.exitLabels)
	}
}

func TestCompletionCrossRegionRejected(t *testing.T) {
	b := New[StateID, EventID, Context]("regions").Initial("root").State("root", func(r *compositionState) {
		r.Type(Parallel)
		r.State("a", func(a *compositionState) {
			a.Initial("af")
			a.State("af", func(f *compositionState) { f.Type(Final) })
			a.OnDone().GoTo("bf")
		})
		r.State("b", func(b *compositionState) { b.Initial("bf"); b.State("bf", func(f *compositionState) { f.Type(Final) }) })
	})
	if err := b.Validate(); err == nil || !strings.Contains(err.Error(), "cross-region") {
		t.Fatalf("want cross-region error, got %v", err)
	}
}

func TestCompletionNestedParallelStructurallyPossible(t *testing.T) {
	b := New[StateID, EventID, Context]("nested").Initial("root").State("root", func(r *compositionState) {
		r.Type(Parallel)
		r.OnDone().GoTo("next")
		r.State("inner", func(p *compositionState) {
			p.Type(Parallel)
			p.State("region", func(c *compositionState) {
				c.Initial("final")
				c.State("final", func(f *compositionState) { f.Type(Final) })
			})
		})
	}).State("next", func(*compositionState) {})
	if err := b.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestBuilderInvokeConflictRetainsOriginal(t *testing.T) {
	service := func(context.Context, Context, func(func(Context) Context)) error { return nil }
	b := completionBuilder(func(s *compositionState) {
		s.Invoke(service, "next", "")
		s.InvokeLabel("original")
		s.Invoke(service, "ready", "")
	})
	if b.Validate() == nil {
		t.Fatal("duplicate invoke accepted")
	}
	if got := b.machine.States["work"].Invoke; got.OnDone != "next" || got.label != "original" {
		t.Fatal("conflict replaced original invoke")
	}
}

func TestMachineInitialReplacement(t *testing.T) {
	b := New[StateID, EventID, Context]("replace-root").Initial("old").State("old", func(*compositionState) {}).State("new", func(*compositionState) {})
	b.Use(func(m *compositionBuilder) { m.ReplaceInitial("old", "new") })
	machine, err := b.BuildWithError()
	if err != nil {
		t.Fatal(err)
	}
	if machine.Initial != "new" {
		t.Fatalf("initial = %q, want new", machine.Initial)
	}
	b.ReplaceInitial("old", "old")
	if b.Validate() == nil {
		t.Fatal("stale replacement expectation accepted")
	}
	if machine.Initial != "new" {
		t.Fatal("invalid replacement discarded last valid initial")
	}
}

func TestReplaceOnDone(t *testing.T) {
	b := completionBuilder(func(s *compositionState) {
		s.OnDone().GoTo("next")
		s.ReplaceOnDone("next").Guard(func(Context) bool { return false }).GoTo("next")
		s.OnDone().GoTo("next")
	})
	if err := b.Validate(); err != nil {
		t.Fatal(err)
	}
	routes := b.machine.States["work"].OnDone
	if len(routes) != 2 || routes[0].Guard == nil || routes[1].Guard != nil {
		t.Fatal("replacement did not produce guarded route and fallback")
	}
}

func TestReplaceOnDoneRejectsWrongDefault(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*compositionState)
	}{
		{"missing", func(*compositionState) {}},
		{"wrong target", func(s *compositionState) { s.OnDone().GoTo("ready") }},
		{"guarded", func(s *compositionState) { s.OnDone().Guard(func(Context) bool { return true }).GoTo("next") }},
		{"multiple", func(s *compositionState) {
			s.OnDone().Guard(func(Context) bool { return false }).GoTo("next")
			s.OnDone().GoTo("next")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := completionBuilder(func(s *compositionState) {
				tt.setup(s)
				before := append([]*TransitionDef[StateID, EventID, Context](nil), s.state.OnDone...)
				s.ReplaceOnDone("next").GoTo("detached")
				if len(before) != len(s.state.OnDone) {
					t.Fatal("invalid replacement changed routes")
				}
				for i := range before {
					if before[i] != s.state.OnDone[i] {
						t.Fatal("invalid replacement changed route")
					}
				}
			})
			if err := b.Validate(); err == nil || !strings.Contains(err.Error(), "OnDone replacement") {
				t.Fatalf("want recorded replacement error, got %v", err)
			}
		})
	}
}

func TestReplaceOnDoneConflictsInEitherOrder(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(map[bool]string{false: "forward", true: "reverse"}[reverse], func(t *testing.T) {
			b := completionBuilder(func(s *compositionState) { s.OnDone().GoTo("next") })
			replace := func(target StateID) func(*compositionBuilder) {
				return func(m *compositionBuilder) {
					m.Extend("work", func(s *compositionState) { s.ReplaceOnDone("next").GoTo(target) })
				}
			}
			first, second := replace("one"), replace("two")
			if reverse {
				first, second = second, first
			}
			b.Use(first, second)
			if err := b.Validate(); err == nil || !strings.Contains(err.Error(), "OnDone replacement") {
				t.Fatalf("want conflict, got %v", err)
			}
			want := StateID("one")
			if reverse {
				want = "two"
			}
			if got := b.machine.States["work"].OnDone; len(got) != 1 || got[0].Target != want {
				t.Fatal("failed replacement changed prior route")
			}
		})
	}
}

func TestReplaceOnDoneRequiresFinalFallback(t *testing.T) {
	b := completionBuilder(func(s *compositionState) {
		s.OnDone().GoTo("next")
		s.ReplaceOnDone("next").Guard(func(Context) bool { return false }).GoTo("next")
	})
	if err := b.Validate(); err == nil || !strings.Contains(err.Error(), "unconditional fallback") {
		t.Fatalf("want fallback error, got %v", err)
	}
}

func TestInvokeLabelRejectsInvalidWrites(t *testing.T) {
	noop := func(context.Context, Context, func(func(Context) Context)) error { return nil }
	for _, test := range []struct {
		name      string
		configure func(*compositionState)
		want      string
	}{
		{"before invoke", func(s *compositionState) { s.InvokeLabel("work"); s.Invoke(noop, "", "") }, "preceding"},
		{"empty", func(s *compositionState) { s.Invoke(noop, "", ""); s.InvokeLabel("") }, "nonempty"},
		{"overwrite", func(s *compositionState) { s.Invoke(noop, "", ""); s.InvokeLabel("original"); s.InvokeLabel("changed") }, "already labeled"},
		{"replacement bypass", func(s *compositionState) {
			s.Invoke(noop, "", "")
			s.InvokeLabel("original")
			s.InvokeLabel("changed")
			s.ReplaceInvoke("changed", noop, "", "")
		}, "already labeled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			b := completionBuilder(test.configure)
			if err := b.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate = %v, want %q", err, test.want)
			}
			if test.name == "overwrite" || test.name == "replacement bypass" {
				if got := b.machine.States["work"].Invoke.label; got != "original" {
					t.Fatalf("label changed to %q", got)
				}
			}
		})
	}
}
