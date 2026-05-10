package gstate

import (
	"fmt"
	"sort"

	md "github.com/TyphonHill/go-mermaid/diagrams/state"
	"github.com/TyphonHill/go-mermaid/diagrams/utils/basediagram"
)

// MermaidThemeName identifies a Mermaid color theme.
type MermaidThemeName = basediagram.ThemeName

// Mermaid theme constants matching the five built-in Mermaid themes.
const (
	MermaidThemeDefault  MermaidThemeName = basediagram.ThemeDefault
	MermaidThemeNeutral  MermaidThemeName = basediagram.ThemeNeutral
	MermaidThemeDark     MermaidThemeName = basediagram.ThemeDark
	MermaidThemeForest   MermaidThemeName = basediagram.ThemeForest
	MermaidThemeBase     MermaidThemeName = basediagram.ThemeBase
)

// mermaidConfig holds optional configuration for Mermaid diagram rendering.
type mermaidConfig struct {
	theme    *MermaidThemeName
	title    string
	fontSize int // 0 means use go-mermaid default (16)
}

// MermaidOption configures Mermaid diagram output.
type MermaidOption func(*mermaidConfig)

// MermaidTheme sets the Mermaid color theme (default, neutral, dark, forest, base).
func MermaidTheme(theme MermaidThemeName) MermaidOption {
	return func(c *mermaidConfig) {
		c.theme = &theme
	}
}

// MermaidTitle sets the diagram title shown in the frontmatter.
func MermaidTitle(title string) MermaidOption {
	return func(c *mermaidConfig) {
		c.title = title
	}
}

// MermaidFontSize sets the base font size in pixels (default 16).
func MermaidFontSize(size int) MermaidOption {
	return func(c *mermaidConfig) {
		c.fontSize = size
	}
}

// ToMermaid converts a Machine to a Mermaid stateDiagram-v2 string.
// Options configure the frontmatter (theme, title, fontSize).
func ToMermaid[S ~string, E ~string, C any](m *Machine[S, E, C], opts ...MermaidOption) string {
	var cfg mermaidConfig
	for _, o := range opts {
		o(&cfg)
	}

	d := md.NewDiagram()

	// Apply options to the diagram's config.
	if cfg.theme != nil {
		d.Config.SetTheme(*cfg.theme)
	}
	if cfg.title != "" {
		d.SetTitle(cfg.title)
	}
	if cfg.fontSize > 0 {
		d.Config.SetFontSize(cfg.fontSize)
	}

	stateMap := map[string]*md.State{}

	topLevel := sortedTopLevel(m)

	// Build states
	for _, s := range topLevel {
		buildState(d, nil, s, m, stateMap)
	}

	// Initial transition
	if m.Initial != "" {
		if target, ok := stateMap[mermaidID(string(m.Initial))]; ok {
			d.AddTransition(nil, target, "")
		}
	}

	// Build transitions in deterministic order
	forEachState(topLevel, func(s *StateDef[S, E, C]) {
		addTransitions(d, s, stateMap)
	})

	return d.String()
}

func sortedTopLevel[S ~string, E ~string, C any](m *Machine[S, E, C]) []*StateDef[S, E, C] {
	var result []*StateDef[S, E, C]
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

func buildState[S ~string, E ~string, C any](
	d *md.Diagram,
	parentMD *md.State,
	def *StateDef[S, E, C],
	m *Machine[S, E, C],
	stateMap map[string]*md.State,
) {
	id := mermaidID(string(def.ID))

	// Final states: add as end-state transition
	if def.Type == Final {
		s := addOrNestState(d, parentMD, id, string(def.ID), md.StateNormal)
		stateMap[id] = s
		// Add end transition
		d.AddTransition(s, nil, "")
		return
	}

	hasChildren := len(def.States) > 0

	var s *md.State
	if hasChildren || def.Type == Parallel {
		// Composite state
		s = addOrNestState(d, parentMD, id, "", md.StateComposite)

		// Initial child
		if def.Initial != "" {
			initState := s.AddNestedState(mermaidID(string(def.Initial)), "", md.StateStart)
			_ = initState
		}

		// History note
		if def.History == Shallow || def.History == Deep {
			label := "[H]"
			if def.History == Deep {
				label = "[H*]"
			}
			s.AddNote(label, md.NoteRight)
		}

		// Children
		children := sortedChildren(def)
		if def.Type == Parallel {
			// Fork/Join pattern for parallel
			forkID := id + "_fork"
			joinID := id + "_join"
			fork := s.AddNestedState(forkID, "", md.StateFork)
			join := s.AddNestedState(joinID, "", md.StateJoin)
			stateMap[forkID] = fork
			stateMap[joinID] = join

			for _, child := range children {
				buildState(d, s, child, m, stateMap)
				childID := mermaidID(string(child.ID))
				if childState, ok := stateMap[childID]; ok {
					d.AddTransition(fork, childState, "")
					d.AddTransition(childState, join, "")
				}
			}
		} else {
			for _, child := range children {
				buildState(d, s, child, m, stateMap)
			}
		}
	} else {
		// Leaf state
		s = addOrNestState(d, parentMD, id, string(def.ID), md.StateNormal)
	}

	stateMap[id] = s

	// Entry/exit notes
	if len(def.Entry) > 0 && len(def.Exit) > 0 {
		s.AddNote("entry / action<br/>exit / action", md.NoteLeft)
	} else if len(def.Entry) > 0 {
		s.AddNote("entry / action", md.NoteLeft)
	} else if len(def.Exit) > 0 {
		s.AddNote("exit / action", md.NoteLeft)
	}
}

func addOrNestState(d *md.Diagram, parent *md.State, id, description string, st md.StateType) *md.State {
	if parent != nil {
		return parent.AddNestedState(id, description, st)
	}
	return d.AddState(id, description, st)
}

func addTransitions[S ~string, E ~string, C any](
	d *md.Diagram,
	def *StateDef[S, E, C],
	stateMap map[string]*md.State,
) {
	fromID := mermaidID(string(def.ID))
	from, ok := stateMap[fromID]
	if !ok {
		return
	}

	// Event transitions (in declaration order)
	for _, event := range def.eventOrder {
		for _, tr := range def.Transitions[event] {
			targetID := mermaidID(string(tr.Target))
			if to, ok := stateMap[targetID]; ok {
				label := transitionLabel(string(event), tr)
				d.AddTransition(from, to, label)
			}
		}
	}

	// Always transitions
	for _, tr := range def.Always {
		targetID := mermaidID(string(tr.Target))
		if to, ok := stateMap[targetID]; ok {
			label := transitionLabel("", tr)
			d.AddTransition(from, to, label)
		}
	}

	// Delayed transitions
	for _, tr := range def.Delayed {
		targetID := mermaidID(string(tr.Target))
		if to, ok := stateMap[targetID]; ok {
			label := fmt.Sprintf("after %s", formatDuration(tr.After))
			if tr.Guard != nil {
				gn := "guard"
				if tr.guardName != "" {
					gn = tr.guardName
				}
				label += fmt.Sprintf(" [%s]", gn)
			}
			d.AddTransition(from, to, label)
		}
	}

	// Invoke
	if def.Invoke != nil {
		if def.Invoke.OnDone != "" {
			targetID := mermaidID(string(def.Invoke.OnDone))
			if to, ok := stateMap[targetID]; ok {
				d.AddTransition(from, to, "done.invoke")
			}
		}
		if def.Invoke.OnError != "" {
			targetID := mermaidID(string(def.Invoke.OnError))
			if to, ok := stateMap[targetID]; ok {
				d.AddTransition(from, to, "error")
			}
		}
	}
}

func transitionLabel[S ~string, E ~string, C any](event string, tr *TransitionDef[S, E, C]) string {
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

// forEachState walks states depth-first in sorted order.
func forEachState[S ~string, E ~string, C any](states []*StateDef[S, E, C], fn func(*StateDef[S, E, C])) {
	for _, s := range states {
		fn(s)
		forEachState(sortedChildren(s), fn)
	}
}

func mermaidID(id string) string {
	// Replace chars that mermaid can't use in identifiers
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
