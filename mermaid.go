package gstate

import (
	"fmt"
	"sort"

	mm "github.com/floodfx/gstate/internal/mermaid"
)

// MermaidThemeName identifies a Mermaid color theme.
type MermaidThemeName string

// Mermaid theme constants matching the five built-in Mermaid themes.
const (
	MermaidThemeDefault MermaidThemeName = "default"
	MermaidThemeNeutral MermaidThemeName = "neutral"
	MermaidThemeDark    MermaidThemeName = "dark"
	MermaidThemeForest  MermaidThemeName = "forest"
	MermaidThemeBase    MermaidThemeName = "base"
)

// mermaidConfig holds optional configuration for Mermaid diagram rendering.
type mermaidConfig struct {
	theme    *MermaidThemeName
	title    string
	fontSize int // 0 means use Mermaid default (16)
}

// MermaidOption configures Mermaid diagram output.
type MermaidOption func(*mermaidConfig)

// MermaidTheme sets the Mermaid color theme.
func MermaidTheme(theme MermaidThemeName) MermaidOption {
	return func(c *mermaidConfig) { c.theme = &theme }
}

// MermaidTitle sets the diagram title shown in the frontmatter.
func MermaidTitle(title string) MermaidOption {
	return func(c *mermaidConfig) { c.title = title }
}

// MermaidFontSize sets the base font size in pixels (default 16).
func MermaidFontSize(size int) MermaidOption {
	return func(c *mermaidConfig) { c.fontSize = size }
}

// ToMermaid converts a Machine to a Mermaid flowchart source string.
// The output is plain Mermaid syntax; downstream consumers (GitHub README,
// mermaid.live, mermaid-cli, IDE plugins) handle the actual rendering.
func ToMermaid[S ~string, E ~string, D Cloner[D]](m *Machine[S, E, D], opts ...MermaidOption) string {
	var cfg mermaidConfig
	for _, o := range opts {
		o(&cfg)
	}

	f := mm.New(mm.DirTB)
	if cfg.title != "" {
		f.Title(cfg.title)
	}
	if cfg.theme != nil {
		f.Theme(string(*cfg.theme))
	} else {
		f.Theme(string(MermaidThemeDefault))
	}
	if cfg.fontSize > 0 {
		f.FontSize(cfg.fontSize)
	} else {
		f.FontSize(16)
	}

	topLevel := sortedTopLevel(m)

	// Initial pseudo-state + transition into the machine's initial state.
	// The label "●" (U+25CF) gives the synthetic start node the standard
	// UML filled-circle visual.
	if m.Initial != "" {
		f.Node("__start", "●", mm.ShapeCircle)
		f.Edge("__start", mermaidID(string(m.Initial)), "", mm.EdgeSolid)
	}

	// Declare states (nodes/subgraphs) in their proper scope.
	for _, s := range topLevel {
		declareState(f, nil, s, m)
	}

	// Emit all transitions at the top level, in deterministic order.
	walkStates(topLevel, func(s *StateDef[S, E, D]) {
		emitTransitions(f, s)
	})

	return f.String()
}

func declareState[S ~string, E ~string, D Cloner[D]](
	root *mm.Flowchart,
	parent *mm.Subgraph,
	def *StateDef[S, E, D],
	m *Machine[S, E, D],
) {
	id := mermaidID(string(def.ID))
	hasChildren := len(def.States) > 0

	// Final atomic state: render as double-circle in the parent scope.
	if def.Type == Final && !hasChildren {
		newNode(root, parent, id, string(def.ID), mm.ShapeDoubleCircle)
		return
	}

	if hasChildren || def.Type == Parallel {
		// Compound or parallel: emit as subgraph; recurse for children.
		sg := newSubgraph(root, parent, id, string(def.ID))
		if def.Type == Parallel {
			sg.Direction(mm.DirLR)
		}
		if def.History == Shallow || def.History == Deep {
			label := "[H]"
			if def.History == Deep {
				label = "[H*]"
			}
			sg.Node(id+"_hist", label, mm.ShapeCircle)
		}
		for _, child := range sortedChildren(def) {
			declareState(root, sg, child, m)
		}
		return
	}

	// Atomic state: stadium-shaped node with entry/exit labels in the description.
	label := buildStateLabel(def, string(def.ID))
	newNode(root, parent, id, label, mm.ShapeStadium)
}

func newNode(root *mm.Flowchart, parent *mm.Subgraph, id, label string, shape mm.NodeShape) *mm.Node {
	if parent != nil {
		return parent.Node(id, label, shape)
	}
	return root.Node(id, label, shape)
}

func newSubgraph(root *mm.Flowchart, parent *mm.Subgraph, id, label string) *mm.Subgraph {
	if parent != nil {
		return parent.Subgraph(id, label)
	}
	return root.Subgraph(id, label)
}

// buildStateLabel produces the visible label for an atomic state, embedding
// entry/exit annotations when the state has Entry/Exit actions.
func buildStateLabel[S ~string, E ~string, D Cloner[D]](def *StateDef[S, E, D], defaultLabel string) string {
	hasEntry := len(def.Entry) > 0
	hasExit := len(def.Exit) > 0
	if !hasEntry && !hasExit {
		return defaultLabel
	}
	label := defaultLabel
	if hasEntry {
		ename := def.entryLabel
		if ename == "" {
			ename = "action"
		}
		label += "<br/>entry / " + ename
	}
	if hasExit {
		xname := def.exitLabel
		if xname == "" {
			xname = "action"
		}
		label += "<br/>exit / " + xname
	}
	return label
}

func emitTransitions[S ~string, E ~string, D Cloner[D]](f *mm.Flowchart, def *StateDef[S, E, D]) {
	from := mermaidID(string(def.ID))

	// Event-driven transitions, in declaration order.
	for _, event := range def.eventOrder {
		for _, tr := range def.Transitions[event] {
			f.Edge(from, mermaidID(string(tr.Target)), transitionLabel(string(event), tr), mm.EdgeSolid)
		}
	}

	// Always transitions. Render with "always" where the event name would
	// go so readers can see the transition is automatic/eventless, rather
	// than an unlabeled arrow that looks like any other connection.
	for _, tr := range def.Always {
		f.Edge(from, mermaidID(string(tr.Target)), transitionLabel("always", tr), mm.EdgeSolid)
	}

	// Delayed transitions.
	for _, tr := range def.Delayed {
		label := fmt.Sprintf("after %s", formatDuration(tr.After))
		if tr.Guard != nil {
			gn := "guard"
			if tr.guardName != "" {
				gn = tr.guardName
			}
			label += fmt.Sprintf(" [%s]", gn)
		}
		f.Edge(from, mermaidID(string(tr.Target)), label, mm.EdgeSolid)
	}

	// Invoke: diamond pseudo-state when both OnDone and OnError are set;
	// direct edge with friendly label otherwise.
	if def.Invoke != nil {
		hasDone := def.Invoke.OnDone != ""
		hasError := def.Invoke.OnError != ""
		switch {
		case hasDone && hasError:
			diamondID := from + "_invoke"
			diamondLabel := def.Invoke.label
			if diamondLabel != "" {
				diamondID = mermaidID(diamondLabel)
			}
			f.Node(diamondID, diamondLabel, mm.ShapeDiamond)
			f.Edge(from, diamondID, "", mm.EdgeSolid)
			f.Edge(diamondID, mermaidID(string(def.Invoke.OnDone)), "invoke.done", mm.EdgeSolid)
			f.Edge(diamondID, mermaidID(string(def.Invoke.OnError)), "invoke.error", mm.EdgeSolid)
		case hasDone:
			label := "invoke.done"
			if def.Invoke.label != "" {
				label += " [" + def.Invoke.label + "]"
			}
			f.Edge(from, mermaidID(string(def.Invoke.OnDone)), label, mm.EdgeSolid)
		case hasError:
			label := "invoke.error"
			if def.Invoke.label != "" {
				label += " [" + def.Invoke.label + "]"
			}
			f.Edge(from, mermaidID(string(def.Invoke.OnError)), label, mm.EdgeSolid)
		}
	}
}

func transitionLabel[S ~string, E ~string, D Cloner[D]](event string, tr *TransitionDef[S, E, D]) string {
	label := event
	if tr.Guard != nil {
		gn := "guard"
		if tr.guardName != "" {
			gn = tr.guardName
		}
		if label != "" {
			label += " "
		}
		label += fmt.Sprintf("[%s]", gn)
	}
	return label
}

func sortedTopLevel[S ~string, E ~string, D Cloner[D]](m *Machine[S, E, D]) []*StateDef[S, E, D] {
	var result []*StateDef[S, E, D]
	for _, s := range m.States {
		if s.parent == "" {
			result = append(result, s)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return string(result[i].ID) < string(result[j].ID)
	})
	return result
}

// walkStates walks states depth-first in sorted order.
func walkStates[S ~string, E ~string, D Cloner[D]](states []*StateDef[S, E, D], fn func(*StateDef[S, E, D])) {
	for _, s := range states {
		fn(s)
		walkStates(sortedChildren(s), fn)
	}
}

// mermaidID sanitizes a state id into a Mermaid-compatible identifier.
func mermaidID(id string) string {
	result := make([]byte, 0, len(id))
	for i := 0; i < len(id); i++ {
		c := id[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
			result = append(result, c)
		} else {
			result = append(result, '_')
		}
	}
	return string(result)
}
