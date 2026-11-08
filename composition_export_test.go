package gstate

import (
	"strings"
	"testing"
)

func TestExportsPreserveComposedLifecycleActions(t *testing.T) {
	m := New[StateID, EventID, Context]("composed-actions").Initial("root").State("root", func(s *StateBuilder[StateID, EventID, Context]) {
		s.Entry(func(c Context) Context { return c })
		s.EntryLabel("loadMemory")
		s.Entry(func(c Context) Context { return c })
		s.EntryLabel("configureThinking")
		s.Entry(func(c Context) Context { return c })
		s.Exit(func(c Context) Context { return c })
		s.ExitLabel("clearMemory")
		s.Exit(func(c Context) Context { return c })
		s.ExitLabel("clearThinking")
	}).Build()
	mermaid := ToMermaid(m)
	for _, want := range []string{"entry / loadMemory<br/>entry / configureThinking<br/>entry / action", "exit / clearMemory<br/>exit / clearThinking"} {
		if !strings.Contains(mermaid, want) {
			t.Errorf("Mermaid missing %q:\n%s", want, mermaid)
		}
	}
	raw, err := ToSCXMLString(m)
	if err != nil {
		t.Fatal(err)
	}
	doc := mustParseSCXML(t, raw)
	root := nodeByID(doc.Children, "root")
	if root == nil || root.OnEntry == nil || root.OnExit == nil {
		t.Fatal("missing lifecycle descriptions")
	}
	if got := logLabels(root.OnEntry.Logs); got != "loadMemory,configureThinking,entry action" {
		t.Errorf("entry descriptions = %q", got)
	}
	if got := logLabels(root.OnExit.Logs); got != "clearMemory,clearThinking" {
		t.Errorf("exit descriptions = %q", got)
	}
}

func logLabels(logs []*SCXMLLog) string {
	labels := make([]string, len(logs))
	for i, log := range logs {
		labels[i] = log.Label
	}
	return strings.Join(labels, ",")
}

func TestExportsDescribeLocalCompletion(t *testing.T) {
	m := New[StateID, EventID, Context]("local-completion").Initial("prepare").State("prepare", func(s *StateBuilder[StateID, EventID, Context]) {
		s.Initial("prepared")
		s.State("prepared", func(f *StateBuilder[StateID, EventID, Context]) {
			f.Type(Final)
			f.Entry(func(c Context) Context { return c })
			f.EntryLabel("commitPreparation")
		})
		s.OnDone().Guard(func(c Context) bool { return c.Count > 0 }).GuardLabel("hasResult").Assign(func(c Context) Context { return c }).ActionLabel("acceptResult").GoTo("ready")
		s.OnDone().GoTo("failed")
	}).State("ready", func(s *StateBuilder[StateID, EventID, Context]) {}).State("failed", func(s *StateBuilder[StateID, EventID, Context]) {}).Build()
	mermaid := ToMermaid(m)
	for _, want := range []string{`prepare -->|"done [hasResult] / acceptResult"| ready`, `prepare -->|"done"| failed`, `prepared(((`, `entry / commitPreparation`} {
		if !strings.Contains(mermaid, want) {
			t.Errorf("Mermaid missing %q:\n%s", want, mermaid)
		}
	}
	raw, err := ToSCXMLString(m)
	if err != nil {
		t.Fatal(err)
	}
	doc := mustParseSCXML(t, raw)
	prepare := nodeByID(doc.Children, "prepare")
	if prepare == nil || len(prepare.Transitions) != 2 {
		t.Fatalf("completion transitions missing: %s", raw)
	}
	guarded, fallback := prepare.Transitions[0], prepare.Transitions[1]
	if guarded.Event != "done.state.prepare" || guarded.Target != "ready" || guarded.Cond != "hasResult" || len(guarded.Assign) != 1 || guarded.Assign[0].Location != "acceptResult" {
		t.Fatalf("guarded completion = %+v", guarded)
	}
	if fallback.Event != "done.state.prepare" || fallback.Target != "failed" || fallback.Cond != "" {
		t.Fatalf("fallback completion = %+v", fallback)
	}
	if strings.Contains(raw, "<donedata") {
		t.Fatal("Go callback must not be exported as completion result data")
	}
	final := nodeByID(doc.Children, "prepared")
	if final == nil || final.OnEntry == nil || logLabels(final.OnEntry.Logs) != "commitPreparation" {
		t.Fatal("missing final entry description")
	}
}

func completionGoldenMachine() *Machine[string, string, goldenCtx] {
	return New[string, string, goldenCtx]("completion").Initial("prepare").State("prepare", func(s *StateBuilder[string, string, goldenCtx]) {
		s.Initial("prepared")
		s.Entry(func(c goldenCtx) goldenCtx { return c })
		s.EntryLabel("loadMemory")
		s.Entry(func(c goldenCtx) goldenCtx { return c })
		s.EntryLabel("configureThinking")
		s.Entry(func(c goldenCtx) goldenCtx { return c })
		s.Exit(func(c goldenCtx) goldenCtx { return c })
		s.ExitLabel("releaseScratch")
		s.State("prepared", func(f *StateBuilder[string, string, goldenCtx]) {
			f.Type(Final)
			f.Entry(func(c goldenCtx) goldenCtx { return c })
			f.EntryLabel("publishResult")
		})
		s.OnDone().Guard(func(c goldenCtx) bool { return true }).GuardLabel("approved").Assign(func(c goldenCtx) goldenCtx { return c }).ActionLabel("acceptResult").GoTo("ready")
		s.OnDone().GoTo("failed")
	}).State("ready", func(s *StateBuilder[string, string, goldenCtx]) {}).State("failed", func(s *StateBuilder[string, string, goldenCtx]) {}).Build()
}
