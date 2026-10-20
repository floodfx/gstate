package gstate

import (
	"strings"
	"testing"
	"time"
)

func exportRegressionMachine() *Machine[string, string, Context] {
	m := New[string, string, Context]("exports").Initial("a-b")
	for _, id := range []string{"a-b", "a_b", "雪", "end", "__start", "__gstate_start", "a-b_invoke"} {
		m.State(id, func(s *StateBuilder[string, string, Context]) {
			s.On("INC").Assign(func(c Context) Context { return c }).ActionLabel("increment")
			s.Always().Guard(func(Context) bool { return false }).Assign(func(c Context) Context { return c }).ActionLabel("automatic")
			s.After(time.Second).Assign(func(c Context) Context { return c }).ActionLabel("timeout")
			s.Invoke(nil, "a_b", "a-b")
			s.InvokeLabel("same")
		})
	}
	m.State("parent", func(s *StateBuilder[string, string, Context]) {
		s.Initial("child")
		s.Entry(func(c Context) Context { return c })
		s.EntryLabel("open")
		s.Exit(func(c Context) Context { return c })
		s.ExitLabel("close")
		s.State("child", func(*StateBuilder[string, string, Context]) {})
	})
	return m.Build()
}

func TestMermaidIdentitiesAndTargetlessLabels(t *testing.T) {
	got := ToMermaid(exportRegressionMachine())
	// Every declaration, including synthetic nodes, has an independent ID.
	seen := map[string]bool{}
	for _, line := range strings.Split(stripFrontmatter(got), "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "-->") || line == "" || line == "end" || strings.HasPrefix(line, "flowchart") || strings.HasPrefix(line, "direction") {
			continue
		}
		id := strings.Fields(line)[0]
		if id == "subgraph" {
			id = strings.Fields(line)[1]
		} else if i := strings.IndexAny(id, "[({"); i >= 0 {
			id = id[:i]
		}
		if seen[id] {
			t.Errorf("duplicate declaration %q", id)
		}
		seen[id] = true
	}
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "-->") && strings.HasSuffix(strings.TrimSpace(line), "|") {
			t.Errorf("dangling edge %q", line)
		}
	}
	for _, label := range []string{"increment", "automatic", "timeout", "entry / open", "exit / close"} {
		if !strings.Contains(got, label) {
			t.Errorf("missing label %q", label)
		}
	}
	if !strings.Contains(got, "--> child") {
		t.Error("missing compound initial edge")
	}
}

func TestSCXMLTimerIdentityAndActionLabels(t *testing.T) {
	m := New[string, string, Context]("timers").Initial("a").State("a", func(s *StateBuilder[string, string, Context]) {
		s.Entry(func(c Context) Context { return c })
		s.EntryLabel("open")
		s.Exit(func(c Context) Context { return c })
		s.ExitLabel("close")
		s.After(time.Second).GoTo("a")
		s.After(2 * time.Second).GoTo("a")
	}).Build()
	doc, err := ToSCXML(m)
	if err != nil {
		t.Fatal(err)
	}
	node := doc.Children[0]
	if node.OnEntry.SendElements[0].Event == node.OnEntry.SendElements[1].Event {
		t.Fatal("timer events collide")
	}
	for i, send := range node.OnEntry.SendElements {
		if node.Transitions[i].Event != send.Event {
			t.Fatal("timer send/transition mismatch")
		}
	}
	got, err := ToSCXMLString(m)
	if err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"open", "close"} {
		if !strings.Contains(got, label) {
			t.Errorf("missing %q", label)
		}
	}
}
