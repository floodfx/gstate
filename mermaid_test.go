package gstate

import (
	"strings"
	"testing"
)

// --- 1. Basic flat machine ---

func TestMermaidBasicFlat(t *testing.T) {
	m := New[StateID, EventID, Context]("toggle").
		Initial("off").
		State("off", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("FLIP").GoTo("on")
		}).
		State("on", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("FLIP").GoTo("off")
		}).
		Build()

	got := ToMermaid(m)

	assertContains(t, got, "stateDiagram-v2")
	assertContains(t, got, "[*] --> off")
	assertContains(t, got, "off --> on: FLIP")
	assertContains(t, got, "on --> off: FLIP")
}

// --- 2. Hierarchical (compound) states ---

func TestMermaidHierarchy(t *testing.T) {
	m := New[StateID, EventID, Context]("hierarchy").
		Initial("parent").
		State("parent", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Initial("child1")
			s.On("EXIT").GoTo("done")
			s.State("child1", func(s *StateBuilder[StateID, EventID, Context]) {
				s.On("NEXT").GoTo("child2")
			})
			s.State("child2", func(s *StateBuilder[StateID, EventID, Context]) {})
		}).
		State("done", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Type(Final)
		}).
		Build()

	got := ToMermaid(m)

	assertContains(t, got, "[*] --> parent")
	assertContains(t, got, "state parent {")
	assertContains(t, got, "[*] --> child1")
	assertContains(t, got, "child1 --> child2: NEXT")
	assertContains(t, got, "parent --> done: EXIT")
	assertContains(t, got, "done --> [*]")
}

// --- 3. Final states ---

func TestMermaidFinal(t *testing.T) {
	m := New[StateID, EventID, Context]("final_test").
		Initial("active").
		State("active", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("DONE").GoTo("finished")
		}).
		State("finished", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Type(Final)
		}).
		Build()

	got := ToMermaid(m)
	assertContains(t, got, "finished --> [*]")
}

// --- 4. Parallel states ---

func TestMermaidParallel(t *testing.T) {
	m := New[StateID, EventID, Context]("par").
		Initial("active").
		State("active", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Type(Parallel)
			s.State("region1", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Initial("r1a")
				s.State("r1a", func(s *StateBuilder[StateID, EventID, Context]) {
					s.On("NEXT").GoTo("r1b")
				})
				s.State("r1b", func(s *StateBuilder[StateID, EventID, Context]) {})
			})
			s.State("region2", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Initial("r2a")
				s.State("r2a", func(s *StateBuilder[StateID, EventID, Context]) {})
			})
		}).
		Build()

	got := ToMermaid(m)
	assertContains(t, got, "state active {")
	assertContains(t, got, "--") // parallel separator
	assertContains(t, got, "state region1 {")
	assertContains(t, got, "state region2 {")
	assertContains(t, got, "r1a --> r1b: NEXT")
}

// --- 5. Guards ---

func TestMermaidGuards(t *testing.T) {
	m := New[StateID, EventID, Context]("guard_test").
		Initial("idle").
		State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("GO").Guard(func(c Context) bool { return true }).GuardLabel("isReady").GoTo("active")
		}).
		State("active", func(s *StateBuilder[StateID, EventID, Context]) {}).
		Build()

	got := ToMermaid(m)
	assertContains(t, got, "idle --> active: GO [isReady]")
}

func TestMermaidGuardNoLabel(t *testing.T) {
	m := New[StateID, EventID, Context]("guard_nolabel").
		Initial("idle").
		State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("GO").Guard(func(c Context) bool { return true }).GoTo("active")
		}).
		State("active", func(s *StateBuilder[StateID, EventID, Context]) {}).
		Build()

	got := ToMermaid(m)
	assertContains(t, got, "idle --> active: GO [guard]")
}

// --- 6. Always (eventless) transitions ---

func TestMermaidAlways(t *testing.T) {
	m := New[StateID, EventID, Context]("always_test").
		Initial("a").
		State("a", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Always().Guard(func(c Context) bool { return true }).GuardLabel("check").GoTo("b")
		}).
		State("b", func(s *StateBuilder[StateID, EventID, Context]) {}).
		Build()

	got := ToMermaid(m)
	assertContains(t, got, "a --> b: [check]")
}

// --- 7. History ---

func TestMermaidHistory(t *testing.T) {
	m := New[StateID, EventID, Context]("history").
		Initial("parent").
		State("parent", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Initial("s1")
			s.History(Shallow)
			s.State("s1", func(s *StateBuilder[StateID, EventID, Context]) {})
			s.State("s2", func(s *StateBuilder[StateID, EventID, Context]) {})
		}).
		Build()

	got := ToMermaid(m)
	assertContains(t, got, "state parent {")
	// History should appear as a note or a [H] state inside parent
	assertContains(t, got, "[H]")
}

func TestMermaidHistoryDeep(t *testing.T) {
	m := New[StateID, EventID, Context]("deep_hist").
		Initial("parent").
		State("parent", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Initial("s1")
			s.History(Deep)
			s.State("s1", func(s *StateBuilder[StateID, EventID, Context]) {})
		}).
		Build()

	got := ToMermaid(m)
	assertContains(t, got, "[H*]")
}

// --- 8. Delayed transitions ---

func TestMermaidDelayed(t *testing.T) {
	m := New[StateID, EventID, Context]("delayed").
		Initial("idle").
		State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
			s.After(0).GoTo("timeout")
		}).
		State("timeout", func(s *StateBuilder[StateID, EventID, Context]) {}).
		Build()

	got := ToMermaid(m)
	assertContains(t, got, "idle --> timeout: after 0ms")
}

// --- 9. Invoke ---

func TestMermaidInvoke(t *testing.T) {
	m := New[StateID, EventID, Context]("invoke_test").
		Initial("loading").
		State("loading", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Invoke(nil, "success", "failure")
		}).
		State("success", func(s *StateBuilder[StateID, EventID, Context]) {}).
		State("failure", func(s *StateBuilder[StateID, EventID, Context]) {}).
		Build()

	got := ToMermaid(m)
	assertContains(t, got, "loading --> success: done.invoke")
	assertContains(t, got, "loading --> failure: error")
}

// --- 10. Transition order preserved ---

func TestMermaidTransitionOrder(t *testing.T) {
	m := New[StateID, EventID, Context]("order").
		Initial("s").
		State("s", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("ZULU").GoTo("s")
			s.On("ALPHA").GoTo("s")
		}).
		Build()

	got := ToMermaid(m)
	zIdx := strings.Index(got, "ZULU")
	aIdx := strings.Index(got, "ALPHA")
	if zIdx < 0 || aIdx < 0 {
		t.Fatal("missing transitions")
	}
	if zIdx > aIdx {
		t.Errorf("ZULU should appear before ALPHA (declaration order), got ZULU at %d, ALPHA at %d", zIdx, aIdx)
	}
}

// --- 11. Deterministic output ---

func TestMermaidDeterministic(t *testing.T) {
	m := New[StateID, EventID, Context]("det").
		Initial("a").
		State("a", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("GO").GoTo("b")
		}).
		State("b", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("BACK").GoTo("a")
		}).
		Build()

	first := ToMermaid(m)
	for i := 0; i < 20; i++ {
		got := ToMermaid(m)
		if got != first {
			t.Fatalf("non-deterministic output on iteration %d", i)
		}
	}
}

// --- 12. Entry/exit shown as notes ---

func TestMermaidEntryExit(t *testing.T) {
	m := New[StateID, EventID, Context]("actions").
		Initial("s").
		State("s", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Entry(func(c Context) Context { return c })
			s.Exit(func(c Context) Context { return c })
		}).
		Build()

	got := ToMermaid(m)
	assertContains(t, got, "entry /")
	assertContains(t, got, "exit /")
}

// --- 13. Options: theme ---

func TestMermaidTheme(t *testing.T) {
	m := New[StateID, EventID, Context]("toggle").
		Initial("off").
		State("off", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("FLIP").GoTo("on")
		}).
		State("on", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("FLIP").GoTo("off")
		}).
		Build()

	got := ToMermaid(m, MermaidTheme(MermaidThemeDark))
	assertContains(t, got, "theme: dark")
}

// --- 14. Options: title ---

func TestMermaidTitle(t *testing.T) {
	m := New[StateID, EventID, Context]("toggle").
		Initial("off").
		State("off", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("FLIP").GoTo("on")
		}).
		State("on", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("FLIP").GoTo("off")
		}).
		Build()

	got := ToMermaid(m, MermaidTitle("Light Switch"))
	assertContains(t, got, "title: Light Switch")
}

// --- 15. Options: font size ---

func TestMermaidFontSize(t *testing.T) {
	m := New[StateID, EventID, Context]("toggle").
		Initial("off").
		State("off", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("FLIP").GoTo("on")
		}).
		State("on", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("FLIP").GoTo("off")
		}).
		Build()

	got := ToMermaid(m, MermaidFontSize(20))
	assertContains(t, got, "fontSize: 20")
}

// --- 16. Options: combine multiple ---

func TestMermaidMultipleOptions(t *testing.T) {
	m := New[StateID, EventID, Context]("toggle").
		Initial("off").
		State("off", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("FLIP").GoTo("on")
		}).
		State("on", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("FLIP").GoTo("off")
		}).
		Build()

	got := ToMermaid(m, MermaidTheme(MermaidThemeForest), MermaidTitle("My Machine"))
	assertContains(t, got, "theme: forest")
	assertContains(t, got, "title: My Machine")
	assertContains(t, got, "stateDiagram-v2")
}

// --- 17. Default config preserved ---

func TestMermaidDefaultConfig(t *testing.T) {
	m := New[StateID, EventID, Context]("toggle").
		Initial("off").
		State("off", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("FLIP").GoTo("on")
		}).
		State("on", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("FLIP").GoTo("off")
		}).
		Build()

	got := ToMermaid(m)
	assertContains(t, got, "theme: default")
	assertContains(t, got, "fontSize: 16")
}

// --- 18. Multiline notes use <br/> not newlines ---

func TestMermaidNoteNoRawNewline(t *testing.T) {
	m := New[StateID, EventID, Context]("actions").
		Initial("s").
		State("s", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Entry(func(c Context) Context { return c })
			s.Exit(func(c Context) Context { return c })
		}).
		Build()

	got := ToMermaid(m)

	// The note text must use <br/> for line breaks, not literal newlines.
	// A raw newline after "note ... of ...:" causes Mermaid to parse the
	// second line as a new statement, creating phantom states.
	assertContains(t, got, "entry / action<br/>exit / action")

	// Must NOT contain a raw newline between entry and exit in the note
	if strings.Contains(got, "entry / action\nexit / action") {
		t.Error("note should use <br/> not raw newline between entry and exit")
	}
}

// --- helpers ---

func assertContains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Errorf("output missing %q\n\ngot:\n%s", want, got)
	}
}
