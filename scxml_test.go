package gstate

import (
	"encoding/xml"
	"strings"
	"testing"
)

// --- Helper: parse SCXML string back to a document for structural assertions ---

func mustParseSCXML(t *testing.T, raw string) *SCXMLDocument {
	t.Helper()
	var doc SCXMLDocument
	if err := xml.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("failed to parse SCXML: %v\n%s", err, raw)
	}
	return &doc
}

// nodeByID finds a descendant node by ID, walking recursively.
func nodeByID(nodes []SCXMLNode, id string) *SCXMLNode {
	for i := range nodes {
		if nodes[i].ID == id {
			return &nodes[i]
		}
		if found := nodeByID(nodes[i].Children, id); found != nil {
			return found
		}
	}
	return nil
}

// collectNodesByKind collects all nodes with a given NodeKind, recursively.
func collectNodesByKind(nodes []SCXMLNode, kind NodeKind) []*SCXMLNode {
	var result []*SCXMLNode
	for i := range nodes {
		if nodes[i].Kind == kind {
			result = append(result, &nodes[i])
		}
		result = append(result, collectNodesByKind(nodes[i].Children, kind)...)
	}
	return result
}

// collectDirectNodesByKind collects nodes with a given NodeKind from a specific slice only (non-recursive).
func collectDirectNodesByKind(nodes []SCXMLNode, kind NodeKind) []*SCXMLNode {
	var result []*SCXMLNode
	for i := range nodes {
		if nodes[i].Kind == kind {
			result = append(result, &nodes[i])
		}
	}
	return result
}

// collectNodesWhere collects nodes matching a predicate, recursively.
func collectNodesWhere(nodes []SCXMLNode, fn func(SCXMLNode) bool) []*SCXMLNode {
	var result []*SCXMLNode
	for i := range nodes {
		if fn(nodes[i]) {
			result = append(result, &nodes[i])
		}
		result = append(result, collectNodesWhere(nodes[i].Children, fn)...)
	}
	return result
}

// collectDirectNodesWhere collects nodes matching a predicate from a specific slice only (non-recursive).
func collectDirectNodesWhere(nodes []SCXMLNode, fn func(SCXMLNode) bool) []*SCXMLNode {
	var result []*SCXMLNode
	for i := range nodes {
		if fn(nodes[i]) {
			result = append(result, &nodes[i])
		}
	}
	return result
}

// historyNodesMatching collects <history> nodes with a specific HistoryMode, recursively.
func historyNodesMatching(nodes []SCXMLNode, mode string) []*SCXMLNode {
	var result []*SCXMLNode
	for i := range nodes {
		if nodes[i].Kind == NodeHistory && nodes[i].HistoryMode == mode {
			result = append(result, &nodes[i])
		}
		result = append(result, historyNodesMatching(nodes[i].Children, mode)...)
	}
	return result
}

// --- 1. Basic flat machine with two atomic states ---

func TestSCXMLBasicFlatMachine(t *testing.T) {
	m := New[StateID, EventID, Context]("toggle").
		Initial("off").
		State("off", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("FLIP").GoTo("on")
		}).
		State("on", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("FLIP").GoTo("off")
		}).
		Build()

	doc, err := ToSCXML(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check root attributes
	if doc.XMLNS != "http://www.w3.org/2005/07/scxml" {
		t.Errorf("expected xmlns, got %s", doc.XMLNS)
	}
	if doc.Version != "1.0" {
		t.Errorf("expected version 1.0, got %s", doc.Version)
	}
	if doc.Name != "toggle" {
		t.Errorf("expected name 'toggle', got %s", doc.Name)
	}
	if doc.Initial != "off" {
		t.Errorf("expected initial 'off', got %s", doc.Initial)
	}

	// Check states using type-safe Kind
	states := collectNodesByKind(doc.Children, NodeState)
	if len(states) != 2 {
		t.Fatalf("expected 2 states, got %d", len(states))
	}

	offState := nodeByID(doc.Children, "off")
	if offState == nil {
		t.Fatal("off state not found")
	}
	if len(offState.Transitions) != 1 {
		t.Fatalf("expected 1 transition from 'off', got %d", len(offState.Transitions))
	}
	if offState.Transitions[0].Event != "FLIP" {
		t.Errorf("expected FLIP event, got %s", offState.Transitions[0].Event)
	}
	if offState.Transitions[0].Target != "on" {
		t.Errorf("expected target 'on', got %s", offState.Transitions[0].Target)
	}

	onState := nodeByID(doc.Children, "on")
	if onState == nil {
		t.Fatal("on state not found")
	}
	if len(onState.Transitions) != 1 {
		t.Fatalf("expected 1 transition from 'on', got %d", len(onState.Transitions))
	}
	if onState.Transitions[0].Target != "off" {
		t.Errorf("expected target 'off', got %s", onState.Transitions[0].Target)
	}

	// Round-trip: marshal to XML and parse back
	xmlBytes, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}
	parsed := mustParseSCXML(t, string(xmlBytes))
	if parsed.Initial != "off" || len(collectNodesByKind(parsed.Children, NodeState)) != 2 {
		t.Error("round-trip structural mismatch")
	}
}

// --- 2. Hierarchical (compound) states ---

func TestSCXMLHierarchicalStates(t *testing.T) {
	m := New[StateID, EventID, Context]("hierarchy").
		Initial("parent").
		State("parent", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Initial("child1")
			s.State("child1", func(s *StateBuilder[StateID, EventID, Context]) {
				s.On("TO_C2").GoTo("child2")
			})
			s.State("child2", func(s *StateBuilder[StateID, EventID, Context]) {
				s.On("TO_C1").GoTo("child1")
			})
		}).
		Build()

	doc, err := ToSCXML(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	topStates := collectDirectNodesByKind(doc.Children, NodeState)
	if len(topStates) != 1 {
		t.Fatalf("expected 1 top-level state, got %d", len(topStates))
	}

	parent := topStates[0]
	if parent.ID != "parent" {
		t.Errorf("expected parent, got %s", parent.ID)
	}
	if parent.Initial != "child1" {
		t.Errorf("expected initial child1, got %s", parent.Initial)
	}

	childStates := collectNodesByKind(parent.Children, NodeState)
	if len(childStates) != 2 {
		t.Fatalf("expected 2 child states, got %d", len(childStates))
	}

	child1 := nodeByID(parent.Children, "child1")
	if child1 == nil || len(child1.Transitions) != 1 || child1.Transitions[0].Target != "child2" {
		t.Error("child1 transition mismatch")
	}

	child2 := nodeByID(parent.Children, "child2")
	if child2 == nil || len(child2.Transitions) != 1 || child2.Transitions[0].Target != "child1" {
		t.Error("child2 transition mismatch")
	}
}

// --- 3. Deeply nested hierarchy ---

func TestSCXMLDeeplyNested(t *testing.T) {
	m := New[StateID, EventID, Context]("deep").
		Initial("a").
		State("a", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Initial("b")
			s.State("b", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Initial("c")
				s.State("c", func(s *StateBuilder[StateID, EventID, Context]) {
					s.On("DONE").GoTo("d")
				})
				s.State("d", func(s *StateBuilder[StateID, EventID, Context]) {
					s.Type(Final)
				})
			})
		}).
		Build()

	doc, err := ToSCXML(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Walk: a → b → [c, d]
	a := nodeByID(doc.Children, "a")
	if a == nil || !a.IsState() {
		t.Fatal("a not found or not a state")
	}
	b := nodeByID(a.Children, "b")
	if b == nil {
		t.Fatal("b not found")
	}
	c := nodeByID(b.Children, "c")
	if c == nil || len(c.Transitions) != 1 {
		t.Error("c transition mismatch")
	}
	d := collectNodesByKind(b.Children, NodeFinal)
	if len(d) != 1 || d[0].ID != "d" {
		t.Error("d should be a final node")
	}
}

// --- 4. Parallel states ---

func TestSCXMLParallelStates(t *testing.T) {
	m := New[StateID, EventID, Context]("parallel_test").
		Initial("active").
		State("active", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Type(Parallel)
			s.State("region1", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Initial("r1a")
				s.State("r1a", func(s *StateBuilder[StateID, EventID, Context]) {})
			})
			s.State("region2", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Initial("r2a")
				s.State("r2a", func(s *StateBuilder[StateID, EventID, Context]) {})
			})
		}).
		Build()

	doc, err := ToSCXML(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The "active" node should itself be a <parallel> element
	active := nodeByID(doc.Children, "active")
	if active == nil {
		t.Fatal("active not found")
	}
	if !active.IsParallel() {
		t.Errorf("expected active to be <parallel>, got <%s>", active.Kind)
	}

	regions := collectDirectNodesByKind(active.Children, NodeState)
	if len(regions) != 2 {
		t.Fatalf("expected 2 regions, got %d", len(regions))
	}

	if regions[0].ID != "region1" || regions[1].ID != "region2" {
		t.Errorf("region IDs mismatch: %s, %s", regions[0].ID, regions[1].ID)
	}

	r1a := nodeByID(active.Children, "r1a")
	r2a := nodeByID(active.Children, "r2a")
	if r1a == nil || r2a == nil {
		t.Error("region leaf states not found")
	}
}

// --- 5. Final states ---

func TestSCXMLFinalStates(t *testing.T) {
	m := New[StateID, EventID, Context]("final_test").
		Initial("active").
		State("active", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Initial("working")
			s.State("working", func(s *StateBuilder[StateID, EventID, Context]) {
				s.On("DONE").GoTo("finished")
			})
			s.State("finished", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Type(Final)
			})
		}).
		State("done", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("RESET").GoTo("active")
		}).
		Build()

	doc, err := ToSCXML(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Find the <final> element with id="finished" using type-safe Kind
	finals := collectNodesByKind(doc.Children, NodeFinal)
	if len(finals) != 1 || finals[0].ID != "finished" {
		t.Errorf("expected one <final> with id='finished', got %d finals", len(finals))
	}
	if !finals[0].IsFinal() {
		t.Error("expected IsFinal() to return true")
	}
}

// --- 6. History states (shallow and deep) ---

func TestSCXMLHistory(t *testing.T) {
	m := New[StateID, EventID, Context]("history_test").
		Initial("parent").
		State("parent", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Initial("s1")
			s.History(Shallow)
			s.State("s1", func(s *StateBuilder[StateID, EventID, Context]) {})
			s.State("s2", func(s *StateBuilder[StateID, EventID, Context]) {})
		}).
		Build()

	doc, err := ToSCXML(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	parent := nodeByID(doc.Children, "parent")
	if parent == nil {
		t.Fatal("parent not found")
	}

	histNodes := historyNodesMatching(parent.Children, "shallow")
	if len(histNodes) != 1 {
		t.Fatalf("expected 1 shallow <history> child, got %d", len(histNodes))
	}
	if !histNodes[0].IsHistory() {
		t.Error("expected IsHistory() to be true")
	}
}

func TestSCXMLHistoryDeep(t *testing.T) {
	m := New[StateID, EventID, Context]("history_deep").
		Initial("parent").
		State("parent", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Initial("s1")
			s.History(Deep)
			s.State("s1", func(s *StateBuilder[StateID, EventID, Context]) {})
		}).
		Build()

	doc, err := ToSCXML(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	parent := nodeByID(doc.Children, "parent")
	histNodes := historyNodesMatching(parent.Children, "deep")
	if len(histNodes) != 1 {
		t.Fatal("expected a deep <history> child")
	}
	if !histNodes[0].IsHistory() {
		t.Error("expected IsHistory() to be true")
	}
}

// --- 7. No history (None) ---

func TestSCXMLNoHistory(t *testing.T) {
	m := New[StateID, EventID, Context]("no_history").
		Initial("parent").
		State("parent", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Initial("s1")
			s.State("s1", func(s *StateBuilder[StateID, EventID, Context]) {})
		}).
		Build()

	doc, err := ToSCXML(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	parent := nodeByID(doc.Children, "parent")
	histNodes := collectNodesWhere(parent.Children, SCXMLNode.IsHistory)
	if len(histNodes) != 0 {
		t.Error("unexpected <history> child when History is None")
	}
}

// --- 8. Transitions with guards ---

func TestSCXMLTransitionsWithGuard(t *testing.T) {
	m := New[StateID, EventID, Context]("guard_test").
		Initial("idle").
		State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("GO").Guard(func(c Context) bool { return true }).GoTo("active")
		}).
		State("active", func(s *StateBuilder[StateID, EventID, Context]) {}).
		Build()

	doc, err := ToSCXML(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	idle := nodeByID(doc.Children, "idle")
	tr := idle.Transitions[0]
	if tr.Cond != "guard" {
		t.Errorf("expected cond='guard' when Guard is present, got '%s'", tr.Cond)
	}
	if tr.Event != "GO" {
		t.Errorf("expected event GO, got '%s'", tr.Event)
	}
}

// --- 9. Transitions without guards ---

func TestSCXMLTransitionsNoGuard(t *testing.T) {
	m := New[StateID, EventID, Context]("noguard_test").
		Initial("idle").
		State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("GO").GoTo("active")
		}).
		State("active", func(s *StateBuilder[StateID, EventID, Context]) {}).
		Build()

	doc, err := ToSCXML(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	idle := nodeByID(doc.Children, "idle")
	tr := idle.Transitions[0]
	if tr.Cond != "" {
		t.Errorf("expected no cond attribute, got '%s'", tr.Cond)
	}
}

// --- 10. Entry and exit actions ---

func TestSCXMLEntryActions(t *testing.T) {
	m := New[StateID, EventID, Context]("entry_test").
		Initial("idle").
		State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Entry(func(c Context) Context { return c })
		}).
		Build()

	doc, err := ToSCXML(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	idle := nodeByID(doc.Children, "idle")
	if idle.OnEntry == nil {
		t.Error("expected <onentry> element when Entry actions exist")
	}
}

func TestSCXMLExitActions(t *testing.T) {
	m := New[StateID, EventID, Context]("exit_test").
		Initial("idle").
		State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Exit(func(c Context) Context { return c })
		}).
		Build()

	doc, err := ToSCXML(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	idle := nodeByID(doc.Children, "idle")
	if idle.OnExit == nil {
		t.Error("expected <onexit> element when Exit actions exist")
	}
}

func TestSCXMLNoActionsWhenEmpty(t *testing.T) {
	m := New[StateID, EventID, Context]("no_actions").
		Initial("idle").
		State("idle", func(s *StateBuilder[StateID, EventID, Context]) {}).
		Build()

	doc, err := ToSCXML(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	idle := nodeByID(doc.Children, "idle")
	if idle.OnEntry != nil {
		t.Error("unexpected <onentry> when no entry actions")
	}
	if idle.OnExit != nil {
		t.Error("unexpected <onexit> when no exit actions")
	}
}

// --- 11. Transition actions (Assign) ---

func TestSCXMLTransitionActions(t *testing.T) {
	m := New[StateID, EventID, Context]("assign_test").
		Initial("idle").
		State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("INC").Assign(func(c Context) Context { return c }).GoTo("idle")
		}).
		Build()

	doc, err := ToSCXML(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	idle := nodeByID(doc.Children, "idle")
	tr := idle.Transitions[0]
	if tr.Assign == nil || len(tr.Assign) == 0 {
		t.Error("expected <assign> element when transition has Action")
	}
}

func TestSCXMLNoAssignWhenEmpty(t *testing.T) {
	m := New[StateID, EventID, Context]("no_assign").
		Initial("idle").
		State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("GO").GoTo("active")
		}).
		State("active", func(s *StateBuilder[StateID, EventID, Context]) {}).
		Build()

	doc, err := ToSCXML(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	idle := nodeByID(doc.Children, "idle")
	tr := idle.Transitions[0]
	if tr.Assign != nil {
		t.Error("unexpected <assign> when no Action")
	}
}

// --- 12. Invoke ---

func TestSCXMLInvoke(t *testing.T) {
	m := New[StateID, EventID, Context]("invoke_test").
		Initial("loading").
		State("loading", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Invoke(nil, "success", "failure")
		}).
		State("success", func(s *StateBuilder[StateID, EventID, Context]) {}).
		State("failure", func(s *StateBuilder[StateID, EventID, Context]) {}).
		Build()

	doc, err := ToSCXML(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	loadingState := nodeByID(doc.Children, "loading")
	if loadingState == nil || loadingState.Invoke == nil {
		t.Fatal("expected <invoke> element")
	}

	// onDone and onError should appear as transitions
	foundDone := false
	foundError := false
	for _, tr := range loadingState.Transitions {
		if tr.Event == "done.invoke.loading" && tr.Target == "success" {
			foundDone = true
		}
		if tr.Event == "error.platform" && tr.Target == "failure" {
			foundError = true
		}
	}
	if !foundDone {
		t.Error("expected done.invoke.loading → success transition")
	}
	if !foundError {
		t.Error("expected error.platform → failure transition")
	}
}

// --- 13. Delayed transitions ---

func TestSCXMLDelayedTransitions(t *testing.T) {
	m := New[StateID, EventID, Context]("delay_test").
		Initial("idle").
		State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
			s.After(0).GoTo("timeout")
		}).
		State("timeout", func(s *StateBuilder[StateID, EventID, Context]) {}).
		Build()

	doc, err := ToSCXML(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	idleState := nodeByID(doc.Children, "idle")
	if idleState == nil {
		t.Fatal("idle not found")
	}

	// Should have <send> with delay in <onentry>
	if idleState.OnEntry == nil || len(idleState.OnEntry.SendElements) == 0 {
		t.Error("expected <send> with delay in <onentry>")
	} else {
		se := idleState.OnEntry.SendElements[0]
		if se.Delay == "" {
			t.Error("expected delay attribute on <send>")
		}
	}

	// Should also have a transition for the delay event
	found := false
	for _, tr := range idleState.Transitions {
		if tr.Target == "timeout" {
			found = true
		}
	}
	if !found {
		t.Error("expected transition to timeout")
	}
}

// --- 14. Always (transient) transitions ---

func TestSCXMLAlwaysTransitions(t *testing.T) {
	m := New[StateID, EventID, Context]("always_test").
		Initial("a").
		State("a", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Always().Guard(func(c Context) bool { return true }).GoTo("b")
		}).
		State("b", func(s *StateBuilder[StateID, EventID, Context]) {}).
		Build()

	doc, err := ToSCXML(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	aState := nodeByID(doc.Children, "a")
	found := false
	for _, tr := range aState.Transitions {
		if tr.Event == "" && tr.Target == "b" && tr.Cond == "guard" {
			found = true
		}
	}
	if !found {
		t.Error("expected eventless transition with cond and target 'b'")
	}
}

// --- 15. Multiple transitions for the same event ---

func TestSCXMLMultipleTransitionsSameEvent(t *testing.T) {
	m := New[StateID, EventID, Context]("multi").
		Initial("idle").
		State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("GO").Guard(func(c Context) bool { return true }).GoTo("a")
			s.On("GO").GoTo("b")
		}).
		State("a", func(s *StateBuilder[StateID, EventID, Context]) {}).
		State("b", func(s *StateBuilder[StateID, EventID, Context]) {}).
		Build()

	doc, err := ToSCXML(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	idleState := nodeByID(doc.Children, "idle")
	goTransitions := 0
	for _, tr := range idleState.Transitions {
		if tr.Event == "GO" {
			goTransitions++
		}
	}
	if goTransitions != 2 {
		t.Fatalf("expected 2 GO transitions, got %d", goTransitions)
	}
}

// --- 16. Full complex machine ---

func TestSCXMLComplexMachine(t *testing.T) {
	m := New[StateID, EventID, Context]("complex").
		Initial("main").
		State("main", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Initial("idle")
			s.Entry(func(c Context) Context { return c })
			s.Exit(func(c Context) Context { return c })
			s.History(Shallow)

			s.State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
				s.On("START").Guard(func(c Context) bool { return true }).GoTo("active")
				s.On("RESET").Assign(func(c Context) Context { return c }).GoTo("idle")
			})

			s.State("active", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Entry(func(c Context) Context { return c })
				s.On("STOP").GoTo("idle")
				s.On("FINISH").GoTo("done")
			})

			s.State("done", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Type(Final)
			})
		}).
		Build()

	doc, err := ToSCXML(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify structure is correct
	main := nodeByID(doc.Children, "main")
	if main == nil {
		t.Fatal("main not found")
	}

	// Should have: history, idle, active, done — using type-safe checks
	hasHistory := len(collectNodesWhere(main.Children, SCXMLNode.IsHistory)) > 0
	hasIdle := nodeByID(main.Children, "idle") != nil
	hasActive := nodeByID(main.Children, "active") != nil
	hasDone := len(collectNodesByKind(main.Children, NodeFinal)) > 0

	if !hasHistory || !hasIdle || !hasActive || !hasDone {
		t.Errorf("missing children: history=%v idle=%v active=%v done=%v",
			hasHistory, hasIdle, hasActive, hasDone)
	}

	// Verify XML round-trip
	xmlBytes, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	parsed := mustParseSCXML(t, string(xmlBytes))
	if parsed.Name != "complex" || parsed.Initial != "main" {
		t.Error("round-trip root attributes mismatch")
	}
}

// --- 17. Namespace validation ---

func TestSCXMLNamespacePresent(t *testing.T) {
	m := New[StateID, EventID, Context]("ns_test").
		Initial("s").
		State("s", func(s *StateBuilder[StateID, EventID, Context]) {}).
		Build()

	doc, err := ToSCXML(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	xmlBytes, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	xmlStr := string(xmlBytes)
	if !strings.Contains(xmlStr, "http://www.w3.org/2005/07/scxml") {
		t.Error("missing SCXML namespace")
	}
	if !strings.Contains(xmlStr, `<scxml `) {
		t.Error("missing root scxml element")
	}
	if !strings.Contains(xmlStr, `version="1.0"`) {
		t.Error("missing version attribute")
	}
}

// --- 18. Empty machine (edge case) ---

func TestSCXMLEmptyMachine(t *testing.T) {
	m := New[StateID, EventID, Context]("empty").
		Build()

	doc, err := ToSCXML(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if doc.Name != "empty" {
		t.Errorf("expected name 'empty', got %s", doc.Name)
	}
	if doc.Initial != "" {
		t.Errorf("expected empty initial, got %s", doc.Initial)
	}
	if len(doc.Children) != 0 {
		t.Errorf("expected 0 children, got %d", len(doc.Children))
	}
}

// --- 19. Machine with no initial set ---

func TestSCXMLNoInitial(t *testing.T) {
	m := New[StateID, EventID, Context]("no_init").
		State("a", func(s *StateBuilder[StateID, EventID, Context]) {}).
		State("b", func(s *StateBuilder[StateID, EventID, Context]) {}).
		Build()

	doc, err := ToSCXML(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if doc.Initial != "" {
		t.Errorf("expected no initial attribute, got '%s'", doc.Initial)
	}
}

// --- 20. String method for convenience ---

func TestSCXMLString(t *testing.T) {
	m := New[StateID, EventID, Context]("str_test").
		Initial("s").
		State("s", func(s *StateBuilder[StateID, EventID, Context]) {}).
		Build()

	str, err := ToSCXMLString(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.HasPrefix(str, `<?xml`) {
		t.Errorf("expected XML header prefix, got: %s", str[:50])
	}
	if !strings.Contains(str, `</scxml>`) {
		t.Errorf("expected scxml closing tag, got: %s", str)
	}
}

// --- 21. Multiple invoke definitions ---

func TestSCXMLMultipleInvokes(t *testing.T) {
	m := New[StateID, EventID, Context]("multi_invoke").
		Initial("a").
		State("a", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Invoke(nil, "done_a", "err_a")
		}).
		State("b", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Invoke(nil, "done_b", "err_b")
		}).
		Build()

	doc, err := ToSCXML(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	a := nodeByID(doc.Children, "a")
	b := nodeByID(doc.Children, "b")
	if a == nil || a.Invoke == nil {
		t.Error("state a: expected invoke")
	}
	if b == nil || b.Invoke == nil {
		t.Error("state b: expected invoke")
	}
}

// --- 22. Parallel inside compound (nested parallel regions) ---

func TestSCXMLParallelInCompound(t *testing.T) {
	m := New[StateID, EventID, Context]("par_in_comp").
		Initial("parent").
		State("parent", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Initial("child")
			s.State("child", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Type(Parallel)
				s.State("r1", func(s *StateBuilder[StateID, EventID, Context]) {
					s.Initial("r1a")
					s.State("r1a", func(s *StateBuilder[StateID, EventID, Context]) {})
				})
				s.State("r2", func(s *StateBuilder[StateID, EventID, Context]) {
					s.Initial("r2a")
					s.State("r2a", func(s *StateBuilder[StateID, EventID, Context]) {})
				})
			})
		}).
		Build()

	doc, err := ToSCXML(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// "child" should be a <parallel> node directly within "parent"
	child := nodeByID(doc.Children, "child")
	if child == nil {
		t.Fatal("child not found")
	}
	if child.Kind != NodeParallel {
		t.Errorf("expected <parallel>, got <%s>", child.Kind)
	}
	regions := collectDirectNodesByKind(child.Children, NodeState)
	if len(regions) != 2 {
		t.Fatalf("expected 2 regions, got %d", len(regions))
	}
}

// --- 23. Entry with multiple actions ---

func TestSCXMLMultipleEntryActions(t *testing.T) {
	m := New[StateID, EventID, Context]("multi_entry").
		Initial("s").
		State("s", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Entry(func(c Context) Context { return c })
			s.Entry(func(c Context) Context { return c })
		}).
		Build()

	doc, err := ToSCXML(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	s := nodeByID(doc.Children, "s")
	if s == nil || s.OnEntry == nil {
		t.Error("expected <onentry>")
	}
}

// --- 24. Multiple exit actions ---

func TestSCXMLMultipleExitActions(t *testing.T) {
	m := New[StateID, EventID, Context]("multi_exit").
		Initial("s").
		State("s", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Exit(func(c Context) Context { return c })
			s.Exit(func(c Context) Context { return c })
		}).
		Build()

	doc, err := ToSCXML(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	s := nodeByID(doc.Children, "s")
	if s == nil || s.OnExit == nil {
		t.Error("expected <onexit>")
	}
}

// --- 25. Multiple always transitions ---

func TestSCXMLMultipleAlways(t *testing.T) {
	m := New[StateID, EventID, Context]("multi_always").
		Initial("s").
		State("s", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Always().Guard(func(c Context) bool { return true }).GoTo("a")
			s.Always().GoTo("b")
		}).
		State("a", func(s *StateBuilder[StateID, EventID, Context]) {}).
		State("b", func(s *StateBuilder[StateID, EventID, Context]) {}).
		Build()

	doc, err := ToSCXML(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	s := nodeByID(doc.Children, "s")
	count := 0
	for _, tr := range s.Transitions {
		if tr.Event == "" {
			count++
		}
	}
	if count != 2 {
		t.Errorf("expected 2 eventless transitions (always), got %d", count)
	}
}

// --- 26. Concurrent document access (thread safety of export) ---

func TestSCXMLConcurrentExport(t *testing.T) {
	m := New[StateID, EventID, Context]("concurrent").
		Initial("s").
		State("s", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("E1").GoTo("s")
			s.On("E2").GoTo("s")
		}).
		Build()

	// Should not panic or race
	errc := make(chan error, 10)
	for i := 0; i < 10; i++ {
		go func() {
			_, err := ToSCXML(m)
			errc <- err
		}()
	}

	for i := 0; i < 10; i++ {
		if err := <-errc; err != nil {
			t.Errorf("concurrent export error: %v", err)
		}
	}
}

// --- 27. XML header (optional, via ToSCXMLBytes) ---

func TestSCXMLBytesIncludesHeader(t *testing.T) {
	m := New[StateID, EventID, Context]("header_test").
		Initial("s").
		State("s", func(s *StateBuilder[StateID, EventID, Context]) {}).
		Build()

	data, err := ToSCXMLBytes(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	str := string(data)
	if !strings.HasPrefix(str, xml.Header) {
		t.Error("expected XML header")
	}
}

// --- 28. Invoke with done/error events in SCXML convention ---

func TestSCXMLInvokeDoneEventFormat(t *testing.T) {
	m := New[StateID, EventID, Context]("invoke_done").
		Initial("l").
		State("l", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Invoke(nil, "ok", "fail")
		}).
		State("ok", func(s *StateBuilder[StateID, EventID, Context]) {}).
		State("fail", func(s *StateBuilder[StateID, EventID, Context]) {}).
		Build()

	doc, err := ToSCXML(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	l := nodeByID(doc.Children, "l")
	foundDone := false
	for _, tr := range l.Transitions {
		if tr.Event == "done.invoke.l" && tr.Target == "ok" {
			foundDone = true
		}
	}
	if !foundDone {
		t.Error("expected done.invoke.l → ok transition")
	}
}

// --- 29. Delayed transition with actual duration ---

func TestSCXMLDelayedWithDuration(t *testing.T) {
	m := New[StateID, EventID, Context]("delay_dur").
		Initial("idle").
		State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
			s.After(0).GoTo("done")
		}).
		State("done", func(s *StateBuilder[StateID, EventID, Context]) {}).
		Build()

	doc, err := ToSCXML(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	idleState := nodeByID(doc.Children, "idle")
	found := false
	if idleState.OnEntry != nil {
		for _, se := range idleState.OnEntry.SendElements {
			if se.Event != "" && se.Delay != "" {
				found = true
			}
		}
	}
	for _, tr := range idleState.Transitions {
		if tr.Target == "done" {
			found = true
		}
	}
	if !found {
		t.Error("delayed transition not represented")
	}
}

// --- 30. Builder guard label support ---

func TestSCXMLGuardLabel(t *testing.T) {
	m := New[StateID, EventID, Context]("guard_label").
		Initial("idle").
		State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("GO").Guard(func(c Context) bool { return true }).GuardLabel("isReady").GoTo("active")
		}).
		State("active", func(s *StateBuilder[StateID, EventID, Context]) {}).
		Build()

	doc, err := ToSCXML(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	idle := nodeByID(doc.Children, "idle")
	tr := idle.Transitions[0]
	if tr.Cond != "isReady" {
		t.Errorf("expected cond='isReady', got '%s'", tr.Cond)
	}
}

func TestSCXMLGuardLabelFallback(t *testing.T) {
	m := New[StateID, EventID, Context]("no_label").
		Initial("idle").
		State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("GO").Guard(func(c Context) bool { return true }).GoTo("active")
		}).
		State("active", func(s *StateBuilder[StateID, EventID, Context]) {}).
		Build()

	doc, err := ToSCXML(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	idle := nodeByID(doc.Children, "idle")
	tr := idle.Transitions[0]
	if tr.Cond != "guard" {
		t.Errorf("expected fallback cond='guard', got '%s'", tr.Cond)
	}
}

// --- 31. Action label support ---

func TestSCXMLActionLabel(t *testing.T) {
	m := New[StateID, EventID, Context]("action_label").
		Initial("idle").
		State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("GO").Assign(func(c Context) Context { return c }).ActionLabel("increment").GoTo("active")
		}).
		State("active", func(s *StateBuilder[StateID, EventID, Context]) {}).
		Build()

	doc, err := ToSCXML(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	idle := nodeByID(doc.Children, "idle")
	tr := idle.Transitions[0]
	if tr.Assign == nil || len(tr.Assign) == 0 {
		t.Fatal("expected assign element")
	}
	if tr.Assign[0].Location != "increment" {
		t.Errorf("expected location='increment', got '%s'", tr.Assign[0].Location)
	}
}

// --- 32. Verify output is valid XML ---

func TestSCXMLValidXML(t *testing.T) {
	m := New[StateID, EventID, Context]("valid_xml").
		Initial("a").
		State("a", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("B").GoTo("b")
		}).
		State("b", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Initial("c")
			s.State("c", func(s *StateBuilder[StateID, EventID, Context]) {
				s.On("D").GoTo("d")
			})
			s.State("d", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Type(Final)
			})
		}).
		Build()

	data, err := ToSCXMLBytes(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify it parses cleanly
	var doc SCXMLDocument
	if err := xml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("produced invalid XML: %v\n%s", err, string(data))
	}
}

// --- 34. Transition ordering is deterministic (sorted by event name) ---

func TestSCXMLTransitionOrderDeterministic(t *testing.T) {
	m := New[StateID, EventID, Context]("order_test").
		Initial("s").
		State("s", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("ZEBRA").GoTo("s")
			s.On("ALPHA").GoTo("s")
			s.On("MIDDLE").GoTo("s")
		}).
		Build()

	// Run multiple times to catch non-determinism
	for i := 0; i < 20; i++ {
		doc, err := ToSCXML(m)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		s := nodeByID(doc.Children, "s")
		if len(s.Transitions) != 3 {
			t.Fatalf("expected 3 transitions, got %d", len(s.Transitions))
		}

		// Transitions should be sorted alphabetically by event name
		if s.Transitions[0].Event != "ALPHA" {
			t.Errorf("iteration %d: expected first transition event ALPHA, got %s", i, s.Transitions[0].Event)
		}
		if s.Transitions[1].Event != "MIDDLE" {
			t.Errorf("iteration %d: expected second transition event MIDDLE, got %s", i, s.Transitions[1].Event)
		}
		if s.Transitions[2].Event != "ZEBRA" {
			t.Errorf("iteration %d: expected third transition event ZEBRA, got %s", i, s.Transitions[2].Event)
		}
	}
}

// --- 35. Delayed transitions with guards/actions use same logic as regular transitions ---

func TestSCXMLDelayedTransitionWithGuardAndAction(t *testing.T) {
	m := New[StateID, EventID, Context]("delay_guard_action").
		Initial("idle").
		State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
			s.After(500_000_000). // 500ms
				Guard(func(c Context) bool { return c.Count > 0 }).
				GuardLabel("hasCount").
				Assign(func(c Context) Context { c.Count++; return c }).
				ActionLabel("increment").
				GoTo("done")
		}).
		State("done", func(s *StateBuilder[StateID, EventID, Context]) {}).
		Build()

	doc, err := ToSCXML(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	idle := nodeByID(doc.Children, "idle")
	if idle == nil {
		t.Fatal("idle not found")
	}

	// Find the delayed transition
	var delayTr *SCXMLTransition
	for _, tr := range idle.Transitions {
		if tr.Event == "_delay.idle" {
			delayTr = tr
			break
		}
	}
	if delayTr == nil {
		t.Fatal("delayed transition not found")
	}

	if delayTr.Target != "done" {
		t.Errorf("expected target 'done', got '%s'", delayTr.Target)
	}
	if delayTr.Cond != "hasCount" {
		t.Errorf("expected cond 'hasCount', got '%s'", delayTr.Cond)
	}
	if delayTr.Assign == nil || len(delayTr.Assign) == 0 {
		t.Fatal("expected assign element")
	}
	if delayTr.Assign[0].Location != "increment" {
		t.Errorf("expected location 'increment', got '%s'", delayTr.Assign[0].Location)
	}
}

// --- 36. History nodes should have an ID ---

func TestSCXMLHistoryNodeHasID(t *testing.T) {
	m := New[StateID, EventID, Context]("hist_id").
		Initial("parent").
		State("parent", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Initial("s1")
			s.History(Shallow)
			s.State("s1", func(s *StateBuilder[StateID, EventID, Context]) {})
			s.State("s2", func(s *StateBuilder[StateID, EventID, Context]) {})
		}).
		Build()

	doc, err := ToSCXML(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	parent := nodeByID(doc.Children, "parent")
	if parent == nil {
		t.Fatal("parent not found")
	}

	histNodes := collectNodesByKind(parent.Children, NodeHistory)
	if len(histNodes) != 1 {
		t.Fatalf("expected 1 history node, got %d", len(histNodes))
	}

	// SCXML spec requires history nodes to have an ID
	if histNodes[0].ID != "parent.hist" {
		t.Errorf("expected history ID 'parent.hist', got '%s'", histNodes[0].ID)
	}

	// Verify it appears in the XML output
	xmlStr, err := ToSCXMLString(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(xmlStr, `id="parent.hist"`) {
		t.Errorf("expected id attribute in history XML, got:\n%s", xmlStr)
	}
}

// --- 37. Parallel states emit <parallel> directly, not wrapped in <state> ---

func TestSCXMLParallelEmitsDirect(t *testing.T) {
	m := New[StateID, EventID, Context]("par_direct").
		Initial("active").
		State("active", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Type(Parallel)
			s.State("r1", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Initial("r1a")
				s.State("r1a", func(s *StateBuilder[StateID, EventID, Context]) {})
			})
			s.State("r2", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Initial("r2a")
				s.State("r2a", func(s *StateBuilder[StateID, EventID, Context]) {})
			})
		}).
		Build()

	doc, err := ToSCXML(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The top-level child should be a <parallel> node directly, not a <state> wrapping a <parallel>
	if len(doc.Children) != 1 {
		t.Fatalf("expected 1 top-level child, got %d", len(doc.Children))
	}

	top := doc.Children[0]
	if top.Kind != NodeParallel {
		t.Errorf("expected top-level node to be <parallel>, got <%s>", top.Kind)
	}
	if top.ID != "active" {
		t.Errorf("expected id 'active', got '%s'", top.ID)
	}

	// Should have 2 region children
	regions := collectDirectNodesByKind(top.Children, NodeState)
	if len(regions) != 2 {
		t.Fatalf("expected 2 regions as direct children, got %d", len(regions))
	}

	// Verify XML output doesn't have <state id="active"><parallel>...</parallel></state>
	xmlStr, err := ToSCXMLString(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(xmlStr, `<state id="active">`) {
		t.Errorf("should not wrap parallel in <state>, got:\n%s", xmlStr)
	}
	if !strings.Contains(xmlStr, `<parallel id="active">`) {
		t.Errorf("expected <parallel id=\"active\">, got:\n%s", xmlStr)
	}
}

// Test parallel with transitions, entry, exit
func TestSCXMLParallelWithTransitionsAndActions(t *testing.T) {
	m := New[StateID, EventID, Context]("par_full").
		Initial("active").
		State("active", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Type(Parallel)
			s.Entry(func(c Context) Context { return c })
			s.Exit(func(c Context) Context { return c })
			s.On("STOP").GoTo("done")
			s.State("r1", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Initial("r1a")
				s.State("r1a", func(s *StateBuilder[StateID, EventID, Context]) {})
			})
		}).
		State("done", func(s *StateBuilder[StateID, EventID, Context]) {}).
		Build()

	doc, err := ToSCXML(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	active := nodeByID(doc.Children, "active")
	if active == nil {
		t.Fatal("active not found")
	}
	if active.Kind != NodeParallel {
		t.Errorf("expected <parallel>, got <%s>", active.Kind)
	}
	if active.OnEntry == nil {
		t.Error("expected <onentry> on parallel node")
	}
	if active.OnExit == nil {
		t.Error("expected <onexit> on parallel node")
	}
	if len(active.Transitions) == 0 {
		t.Error("expected transitions on parallel node")
	}
}

// --- 38. Empty assign location should not emit location attribute ---

func TestSCXMLAssignEmptyLocationOmitted(t *testing.T) {
	m := New[StateID, EventID, Context]("assign_empty").
		Initial("idle").
		State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
			// Action with no label — should produce <assign/> without location attr
			s.On("GO").Assign(func(c Context) Context { return c }).GoTo("idle")
		}).
		Build()

	doc, err := ToSCXML(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	idle := nodeByID(doc.Children, "idle")
	tr := idle.Transitions[0]
	if tr.Assign == nil || len(tr.Assign) == 0 {
		t.Fatal("expected assign element")
	}

	// Location should be empty string (omitempty will handle XML output)
	if tr.Assign[0].Location != "" {
		t.Errorf("expected empty location, got '%s'", tr.Assign[0].Location)
	}

	// Verify the XML output doesn't contain location=""
	xmlStr, err := ToSCXMLString(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(xmlStr, `location=""`) {
		t.Errorf("should not emit empty location attribute, got:\n%s", xmlStr)
	}
}

// --- 33. No extra whitespace or attributes ---

func TestSCXMLNoSpuriousAttributes(t *testing.T) {
	m := New[StateID, EventID, Context]("clean").
		Initial("s").
		State("s", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("GO").GoTo("s")
		}).
		Build()

	data, err := ToSCXMLString(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check that xmlns appears only once
	occurrences := strings.Count(data, "xmlns")
	if occurrences > 1 {
		t.Errorf("expected at most 1 xmlns declaration, got %d", occurrences)
	}
}
