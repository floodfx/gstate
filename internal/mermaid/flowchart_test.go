package mermaid

import (
	"strings"
	"testing"

	mc "github.com/sammcj/mermaid-check"
)

// assertParses asserts the given mermaid source is accepted by the
// pure-Go mermaid-check parser. Use after any non-trivial String() call to
// confirm the emitted output is syntactically valid Mermaid.
//
// YAML frontmatter is stripped before parsing — mermaid-check v0.0.4 doesn't
// recognize the `--- ... ---` config block yet (real mermaid-js does). The
// truth tier validates frontmatter end-to-end.
func assertParses(t *testing.T, source string) {
	t.Helper()
	body := stripFrontmatter(source)
	if _, err := mc.ParseFlowchart(body); err != nil {
		t.Errorf("mermaid-check rejected emitted flowchart: %v\n\nsource (frontmatter stripped):\n%s", err, body)
	}
}

func stripFrontmatter(s string) string {
	if !strings.HasPrefix(s, "---\n") {
		return s
	}
	rest := s[len("---\n"):]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		return s
	}
	return rest[end+len("\n---\n"):]
}

func TestEmptyFlowchart(t *testing.T) {
	got := New(DirTB).String()
	want := "flowchart TB\n"
	if got != want {
		t.Errorf("empty flowchart\n got: %q\nwant: %q", got, want)
	}
}

func TestDirectionless(t *testing.T) {
	got := New(DirNone).String()
	if got != "flowchart\n" {
		t.Errorf("DirNone should emit bare `flowchart`, got: %q", got)
	}
}

func TestFrontmatter(t *testing.T) {
	got := New(DirLR).Title("My Diagram").Theme("dark").FontSize(20).String()
	for _, want := range []string{
		"---\n",
		"title: My Diagram\n",
		"config:\n",
		"  theme: dark\n",
		"  themeVariables:\n",
		"    fontSize: 20px\n",
		"flowchart LR\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("frontmatter missing %q\ngot:\n%s", want, got)
		}
	}
}

func TestNodeShapes(t *testing.T) {
	type tc struct {
		shape NodeShape
		want  string
	}
	for _, c := range []tc{
		{ShapeRect, `A["foo"]`},
		{ShapeRound, `A("foo")`},
		{ShapeStadium, `A(["foo"])`},
		{ShapeSubroutine, `A[["foo"]]`},
		{ShapeCylinder, `A[("foo")]`},
		{ShapeCircle, `A(("foo"))`},
		{ShapeDoubleCircle, `A((("foo")))`},
		{ShapeAsymmetric, `A>"foo"]`},
		{ShapeDiamond, `A{"foo"}`},
		{ShapeHexagon, `A{{"foo"}}`},
		{ShapeTrapezoid, `A[/"foo"/]`},
		{ShapeInvTrapezoid, `A[\"foo"\]`},
	} {
		f := New(DirTB)
		f.Node("A", "foo", c.shape)
		got := f.String()
		if !strings.Contains(got, c.want) {
			t.Errorf("shape %d: output missing %q\ngot: %s", c.shape, c.want, got)
		}
	}
}

func TestNodeNoLabelRect(t *testing.T) {
	got := New(DirTB).Node("A", "", ShapeRect).label
	_ = got
	out := New(DirTB)
	out.Node("foo", "", ShapeRect)
	s := out.String()
	if !strings.Contains(s, "    foo\n") {
		t.Errorf("rect node with empty label should emit bare id, got: %s", s)
	}
}

func TestNodeNoLabelShapedUsesID(t *testing.T) {
	out := New(DirTB)
	out.Node("foo", "", ShapeStadium)
	s := out.String()
	if !strings.Contains(s, `foo(["foo"])`) {
		t.Errorf("stadium node with empty label should use id as label, got: %s", s)
	}
}

func TestEdgeStyles(t *testing.T) {
	for _, c := range []struct {
		style EdgeStyle
		label string
		want  string
	}{
		{EdgeSolid, "", "A --> B"},
		{EdgeSolid, "GO", `A -->|"GO"| B`},
		{EdgeDotted, "", "A -.-> B"},
		{EdgeDotted, "later", `A -. "later" .-> B`},
		{EdgeThick, "", "A ==> B"},
		{EdgeThick, "boom", `A == "boom" ==> B`},
	} {
		f := New(DirTB)
		f.Edge("A", "B", c.label, c.style)
		got := f.String()
		if !strings.Contains(got, c.want) {
			t.Errorf("edge %v %q: want %q in:\n%s", c.style, c.label, c.want, got)
		}
	}
}

func TestSubgraph(t *testing.T) {
	f := New(DirTB)
	sg := f.Subgraph("parent", "Parent Label")
	sg.Direction(DirLR)
	sg.Node("child1", "", ShapeStadium)
	sg.Edge("child1", "child2", "NEXT", EdgeSolid)
	got := f.String()
	for _, want := range []string{
		`subgraph parent ["Parent Label"]`,
		"        direction LR",
		`        child1(["child1"])`,
		`        child1 -->|"NEXT"| child2`,
		"    end",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("subgraph output missing %q\ngot:\n%s", want, got)
		}
	}
}

func TestSubgraphNoLabel(t *testing.T) {
	f := New(DirTB)
	f.Subgraph("solo", "")
	got := f.String()
	if !strings.Contains(got, "subgraph solo\n") {
		t.Errorf("subgraph without label should emit bare id, got:\n%s", got)
	}
}

func TestSubgraphNestingBalanced(t *testing.T) {
	f := New(DirTB)
	outer := f.Subgraph("outer", "")
	inner := outer.Subgraph("inner", "")
	inner.Node("leaf", "", ShapeRect)
	got := f.String()
	subgraphs := strings.Count(got, "subgraph ")
	ends := strings.Count(got, "end\n")
	if subgraphs != 2 {
		t.Errorf("want 2 subgraph declarations, got %d in:\n%s", subgraphs, got)
	}
	if ends != 2 {
		t.Errorf("want 2 `end` lines, got %d in:\n%s", ends, got)
	}
}

func TestClassDef(t *testing.T) {
	f := New(DirTB).ClassDef("invokeService", "fill:#eef,stroke:#88c")
	f.Node("A", "label", ShapeDiamond).Class("invokeService")
	got := f.String()
	if !strings.Contains(got, "    classDef invokeService fill:#eef,stroke:#88c\n") {
		t.Errorf("missing classDef line in:\n%s", got)
	}
	if !strings.Contains(got, `A{"label"}:::invokeService`) {
		t.Errorf("missing :::invokeService tag on node in:\n%s", got)
	}
}

func TestMultipleClassesOnNode(t *testing.T) {
	f := New(DirTB)
	f.Node("A", "", ShapeRect).Class("foo").Class("bar")
	got := f.String()
	if !strings.Contains(got, "A:::foo:::bar\n") {
		t.Errorf("expected chained classes, got:\n%s", got)
	}
}

func TestQuoteEscapesInnerQuote(t *testing.T) {
	if got, want := quote(`he said "hi"`), `"he said #quot;hi#quot;"`; got != want {
		t.Errorf("quote escape\n got: %s\nwant: %s", got, want)
	}
}

func TestDeterministicEmission(t *testing.T) {
	build := func() string {
		f := New(DirTB).ClassDef("err", "stroke:red")
		f.Node("A", "", ShapeStadium)
		f.Node("B", "", ShapeDiamond).Class("err")
		f.Edge("A", "B", "GO", EdgeSolid)
		f.Edge("B", "A", "BACK", EdgeSolid)
		return f.String()
	}
	first := build()
	for i := 0; i < 10; i++ {
		if got := build(); got != first {
			t.Fatalf("non-deterministic on iter %d", i)
		}
	}
}

// TestRepresentativeFlowchartParses exercises every primitive in one diagram
// and confirms mermaid-check accepts the emitted output. This is the smoke
// test for spec conformance of the emitter itself.
func TestRepresentativeFlowchartParses(t *testing.T) {
	f := New(DirTB).Title("Test").Theme("default").FontSize(16)
	f.ClassDef("invokeService", "fill:#eef,stroke:#88c")
	f.ClassDef("invokeError", "stroke:#c33,color:#c33")
	f.Node("start", "", ShapeCircle)
	f.Node("idle", "idle<br/>entry / load", ShapeStadium)
	f.Node("active", "", ShapeStadium)
	f.Node("done", "done", ShapeDoubleCircle)
	f.Node("svc", "call_llm", ShapeDiamond).Class("invokeService")
	f.Edge("start", "idle", "", EdgeSolid)
	f.Edge("idle", "active", "GO [isReady]", EdgeSolid)
	f.Edge("active", "svc", "", EdgeSolid)
	f.Edge("svc", "done", "invoke.done", EdgeSolid)
	f.Edge("svc", "failed", "invoke.error", EdgeSolid)
	f.Node("failed", "", ShapeStadium).Class("invokeError")
	sg := f.Subgraph("parent", "parent")
	sg.Direction(DirLR)
	sg.Node("c1", "", ShapeStadium)
	sg.Node("c2", "", ShapeStadium)
	sg.Edge("c1", "c2", "NEXT", EdgeSolid)
	assertParses(t, f.String())
}
