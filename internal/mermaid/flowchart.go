// Package mermaid emits Mermaid flowchart diagram source strings.
//
// This package only emits syntax — it never parses or renders. Downstream
// consumers (GitHub README, mermaid.live, mermaid-cli) do the actual rendering.
//
// The emitter is intentionally minimal and covers only the flowchart features
// gstate needs: shapes, edges, subgraphs, classDef styling, and frontmatter.
package mermaid

import (
	"fmt"
	"strings"
)

// Direction is the layout direction of a flowchart or subgraph.
type Direction string

// Direction constants from the Mermaid flowchart grammar.
const (
	DirNone Direction = ""
	DirTB   Direction = "TB"
	DirTD   Direction = "TD"
	DirBT   Direction = "BT"
	DirRL   Direction = "RL"
	DirLR   Direction = "LR"
)

// NodeShape selects the rendered shape of a node.
type NodeShape int

// Node shape constants matching Mermaid flowchart grammar shape tokens.
const (
	ShapeRect         NodeShape = iota // A[text]
	ShapeRound                         // A(text)
	ShapeStadium                       // A([text])
	ShapeSubroutine                    // A[[text]]
	ShapeCylinder                      // A[(text)]
	ShapeCircle                        // A((text))
	ShapeDoubleCircle                  // A(((text)))
	ShapeAsymmetric                    // A>text]
	ShapeDiamond                       // A{text}
	ShapeHexagon                       // A{{text}}
	ShapeTrapezoid                     // A[/text/]
	ShapeInvTrapezoid                  // A[\text\]
)

// EdgeStyle selects the rendered style of an edge.
type EdgeStyle int

// Edge style constants.
const (
	EdgeSolid  EdgeStyle = iota // -->
	EdgeDotted                  // -.->
	EdgeThick                   // ==>
)

// Flowchart is the top-level diagram.
type Flowchart struct {
	title    string
	theme    string
	fontSize int
	dir      Direction
	classes  []classDefSpec
	body     []element
}

// New creates a new Flowchart with the given top-level direction.
// Pass DirNone to emit a `flowchart` declaration without an explicit direction.
func New(dir Direction) *Flowchart {
	return &Flowchart{dir: dir}
}

// Title sets the diagram title in the frontmatter.
func (f *Flowchart) Title(s string) *Flowchart { f.title = s; return f }

// Theme sets the Mermaid theme name (default, neutral, dark, forest, base).
func (f *Flowchart) Theme(name string) *Flowchart { f.theme = name; return f }

// FontSize sets the base font size in pixels. Zero means "use the Mermaid default".
func (f *Flowchart) FontSize(px int) *Flowchart { f.fontSize = px; return f }

// Node appends a node declaration and returns it.
// An empty label emits just the bare id (Mermaid uses the id as the visible text).
func (f *Flowchart) Node(id, label string, shape NodeShape) *Node {
	n := &Node{id: id, label: label, shape: shape}
	f.body = append(f.body, n)
	return n
}

// Edge appends an edge declaration and returns it.
// An empty label emits an unlabeled arrow.
func (f *Flowchart) Edge(from, to, label string, style EdgeStyle) *Edge {
	e := &Edge{from: from, to: to, label: label, style: style}
	f.body = append(f.body, e)
	return e
}

// Subgraph appends a subgraph and returns it.
// An empty label emits the bare id as the subgraph's visible text.
func (f *Flowchart) Subgraph(id, label string) *Subgraph {
	sg := &Subgraph{id: id, label: label}
	f.body = append(f.body, sg)
	return sg
}

// ClassDef declares a CSS class usable on nodes via Node.Class. Diagram-global.
// Each unique name should be declared only once; duplicate names emit duplicate lines.
func (f *Flowchart) ClassDef(name, css string) *Flowchart {
	f.classes = append(f.classes, classDefSpec{name: name, css: css})
	return f
}

// Node is a single graph node.
type Node struct {
	id      string
	label   string
	shape   NodeShape
	classes []string
}

// Class tags this node with a previously-declared classDef name.
// Multiple classes may be applied; they emit on the same `:::a:::b` chain.
func (n *Node) Class(name string) *Node {
	n.classes = append(n.classes, name)
	return n
}

// Edge is a single directed connection between two nodes.
type Edge struct {
	from, to, label string
	style           EdgeStyle
}

// Subgraph nests nodes and edges inside a labeled group with its own direction.
type Subgraph struct {
	id      string
	label   string
	dir     Direction
	body    []element
	classes []string
}

// Direction sets the layout direction for elements inside this subgraph.
func (s *Subgraph) Direction(d Direction) *Subgraph { s.dir = d; return s }

// Node appends a node inside this subgraph.
func (s *Subgraph) Node(id, label string, shape NodeShape) *Node {
	n := &Node{id: id, label: label, shape: shape}
	s.body = append(s.body, n)
	return n
}

// Edge appends an edge inside this subgraph.
func (s *Subgraph) Edge(from, to, label string, style EdgeStyle) *Edge {
	e := &Edge{from: from, to: to, label: label, style: style}
	s.body = append(s.body, e)
	return e
}

// Subgraph appends a nested subgraph.
func (s *Subgraph) Subgraph(id, label string) *Subgraph {
	sg := &Subgraph{id: id, label: label}
	s.body = append(s.body, sg)
	return sg
}

// Class tags this subgraph with a previously-declared classDef name.
// (Mermaid supports class-on-subgraph for some renderers; emitted as `class id name`.)
func (s *Subgraph) Class(name string) *Subgraph {
	s.classes = append(s.classes, name)
	return s
}

type classDefSpec struct{ name, css string }

type element interface {
	emit(sb *strings.Builder, indent string)
}

// String renders the full Mermaid source.
func (f *Flowchart) String() string {
	var sb strings.Builder
	f.emitFrontmatter(&sb)
	if f.dir != DirNone {
		fmt.Fprintf(&sb, "flowchart %s\n", f.dir)
	} else {
		sb.WriteString("flowchart\n")
	}
	for _, c := range f.classes {
		fmt.Fprintf(&sb, "    classDef %s %s\n", c.name, c.css)
	}
	for _, el := range f.body {
		el.emit(&sb, "    ")
	}
	return sb.String()
}

func (f *Flowchart) emitFrontmatter(sb *strings.Builder) {
	if f.title == "" && f.theme == "" && f.fontSize == 0 {
		return
	}
	sb.WriteString("---\n")
	if f.title != "" {
		fmt.Fprintf(sb, "title: %s\n", f.title)
	}
	if f.theme != "" || f.fontSize > 0 {
		sb.WriteString("config:\n")
		if f.theme != "" {
			fmt.Fprintf(sb, "  theme: %s\n", f.theme)
		}
		if f.fontSize > 0 {
			sb.WriteString("  themeVariables:\n")
			fmt.Fprintf(sb, "    fontSize: %dpx\n", f.fontSize)
		}
	}
	sb.WriteString("---\n")
}

func (n *Node) emit(sb *strings.Builder, indent string) {
	sb.WriteString(indent)
	sb.WriteString(nodeDecl(n.id, n.label, n.shape))
	for _, c := range n.classes {
		sb.WriteString(":::")
		sb.WriteString(c)
	}
	sb.WriteString("\n")
}

func nodeDecl(id, label string, shape NodeShape) string {
	if label == "" && shape == ShapeRect {
		return id
	}
	if label == "" {
		// Use the id as the visible text for shaped nodes without an explicit label.
		label = id
	}
	open, close := shapeBrackets(shape)
	return id + open + quote(label) + close
}

// shapeBrackets returns the open/close delimiters for a node shape per the
// Mermaid flowchart grammar (see flow.jison: SQS/SQE, PS/PE, STADIUMSTART, etc.).
func shapeBrackets(shape NodeShape) (string, string) {
	switch shape {
	case ShapeRect:
		return "[", "]"
	case ShapeRound:
		return "(", ")"
	case ShapeStadium:
		return "([", "])"
	case ShapeSubroutine:
		return "[[", "]]"
	case ShapeCylinder:
		return "[(", ")]"
	case ShapeCircle:
		return "((", "))"
	case ShapeDoubleCircle:
		return "(((", ")))"
	case ShapeAsymmetric:
		return ">", "]"
	case ShapeDiamond:
		return "{", "}"
	case ShapeHexagon:
		return "{{", "}}"
	case ShapeTrapezoid:
		return "[/", "/]"
	case ShapeInvTrapezoid:
		return "[\\", "\\]"
	default:
		return "[", "]"
	}
}

func (e *Edge) emit(sb *strings.Builder, indent string) {
	sb.WriteString(indent)
	sb.WriteString(e.from)
	sb.WriteString(" ")
	sb.WriteString(arrowFor(e.style, e.label))
	sb.WriteString(" ")
	sb.WriteString(e.to)
	sb.WriteString("\n")
}

func arrowFor(style EdgeStyle, label string) string {
	if label == "" {
		switch style {
		case EdgeDotted:
			return "-.->"
		case EdgeThick:
			return "==>"
		default:
			return "-->"
		}
	}
	// Mermaid pipe-delimited label form is solid-only.
	// Dotted/thick use inline form: -. label .->  and  == label ==>.
	switch style {
	case EdgeDotted:
		return "-. " + quote(label) + " .->"
	case EdgeThick:
		return "== " + quote(label) + " ==>"
	default:
		return "-->|" + quote(label) + "|"
	}
}

func (s *Subgraph) emit(sb *strings.Builder, indent string) {
	if s.label == "" {
		fmt.Fprintf(sb, "%ssubgraph %s\n", indent, s.id)
	} else {
		fmt.Fprintf(sb, "%ssubgraph %s [%s]\n", indent, s.id, quote(s.label))
	}
	inner := indent + "    "
	if s.dir != DirNone {
		fmt.Fprintf(sb, "%sdirection %s\n", inner, s.dir)
	}
	for _, el := range s.body {
		el.emit(sb, inner)
	}
	for _, c := range s.classes {
		fmt.Fprintf(sb, "%sclass %s %s\n", inner, s.id, c)
	}
	fmt.Fprintf(sb, "%send\n", indent)
}

// quote wraps text in double-quotes for use inside shape or edge labels.
// Inner double-quotes are entity-encoded per Mermaid convention.
func quote(text string) string {
	if strings.ContainsRune(text, '"') {
		text = strings.ReplaceAll(text, `"`, "#quot;")
	}
	return `"` + text + `"`
}
