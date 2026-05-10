package gstate

import (
	"encoding/xml"
	"fmt"
	"sort"
	"time"
)

// NodeKind identifies the XML element name of an SCXML node.
type NodeKind string

const (
	NodeState    NodeKind = "state"
	NodeFinal    NodeKind = "final"
	NodeParallel NodeKind = "parallel"
	NodeHistory  NodeKind = "history"
)

// SCXMLDocument represents the root <scxml> element.
type SCXMLDocument struct {
	XMLName  xml.Name    `xml:"scxml"`
	XMLNS    string      `xml:"xmlns,attr"`
	Version  string      `xml:"version,attr"`
	Name     string      `xml:"name,attr"`
	Initial  string      `xml:"initial,attr,omitempty"`
	Children []SCXMLNode `xml:",any"`
}

// SCXMLNode represents a child element: <state>, <final>, <parallel>, or <history>.
// The Kind field determines the element name during marshaling.
type SCXMLNode struct {
	Kind        NodeKind           `xml:"-"`
	ID          string             `xml:"id,attr,omitempty"`
	Initial     string             `xml:"initial,attr,omitempty"`
	HistoryMode  string             `xml:"type,attr,omitempty"`
	OnEntry     *SCXMLOnEntry      `xml:"onentry,omitempty"`
	OnExit      *SCXMLOnExit       `xml:"onexit,omitempty"`
	Transitions []*SCXMLTransition `xml:"transition,omitempty"`
	Invoke      *SCXMLInvoke       `xml:"invoke,omitempty"`
	Children    []SCXMLNode        `xml:",any"`
}

// MarshalXML implements xml.Marshaler using Kind as the element name.
func (n SCXMLNode) MarshalXML(enc *xml.Encoder, start xml.StartElement) error {
	if n.Kind == "" {
		return fmt.Errorf("SCXMLNode has zero Kind")
	}
	start.Name.Local = string(n.Kind)

	// Build attributes manually so we control which appear
	attrs := make([]xml.Attr, 0)
	if n.ID != "" {
		attrs = append(attrs, xml.Attr{Name: xml.Name{Local: "id"}, Value: n.ID})
	}
	if n.Initial != "" {
		attrs = append(attrs, xml.Attr{Name: xml.Name{Local: "initial"}, Value: n.Initial})
	}
	if n.HistoryMode != "" {
		attrs = append(attrs, xml.Attr{Name: xml.Name{Local: "type"}, Value: n.HistoryMode})
	}
	start.Attr = attrs

	if err := enc.EncodeToken(start); err != nil {
		return err
	}

	if n.OnEntry != nil {
		if err := enc.EncodeElement(n.OnEntry, xml.StartElement{Name: xml.Name{Local: "onentry"}}); err != nil {
			return err
		}
	}
	if n.OnExit != nil {
		if err := enc.EncodeElement(n.OnExit, xml.StartElement{Name: xml.Name{Local: "onexit"}}); err != nil {
			return err
		}
	}
	for _, tr := range n.Transitions {
		if err := enc.Encode(tr); err != nil {
			return err
		}
	}
	if n.Invoke != nil {
		if err := enc.Encode(n.Invoke); err != nil {
			return err
		}
	}
	for _, child := range n.Children {
		if err := enc.Encode(child); err != nil {
			return err
		}
	}

	return enc.EncodeToken(start.End())
}

// UnmarshalXML implements xml.Unmarshaler, setting Kind from the element name.
func (n *SCXMLNode) UnmarshalXML(dec *xml.Decoder, start xml.StartElement) error {
	n.Kind = NodeKind(start.Name.Local)

	for _, attr := range start.Attr {
		switch attr.Name.Local {
		case "id":
			n.ID = attr.Value
		case "initial":
			n.Initial = attr.Value
		case "type":
			n.HistoryMode = attr.Value
		}
	}

	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "onentry":
				n.OnEntry = &SCXMLOnEntry{}
				if err := dec.DecodeElement(n.OnEntry, &t); err != nil {
					return err
				}
			case "onexit":
				n.OnExit = &SCXMLOnExit{}
				if err := dec.DecodeElement(n.OnExit, &t); err != nil {
					return err
				}
			case "transition":
				tr := &SCXMLTransition{}
				if err := dec.DecodeElement(tr, &t); err != nil {
					return err
				}
				n.Transitions = append(n.Transitions, tr)
			case "invoke":
				n.Invoke = &SCXMLInvoke{}
				if err := dec.DecodeElement(n.Invoke, &t); err != nil {
					return err
				}
			case "state", "final", "parallel", "history":
				child := SCXMLNode{}
				if err := dec.DecodeElement(&child, &t); err != nil {
					return err
				}
				n.Children = append(n.Children, child)
			default:
				// Skip unknown elements (datamodel, data, raise, initial, send, conf:*, etc.)
				if err := dec.Skip(); err != nil {
					return err
				}
			}
		case xml.EndElement:
			return nil
		}
	}
}

// IsHistory reports whether the node is a history pseudo-state.
func (n SCXMLNode) IsHistory() bool { return n.Kind == NodeHistory }

// IsState reports whether the node is a regular <state>.
func (n SCXMLNode) IsState() bool { return n.Kind == NodeState }

// IsFinal reports whether the node is a <final> state.
func (n SCXMLNode) IsFinal() bool { return n.Kind == NodeFinal }

// IsParallel reports whether the node is a <parallel> wrapper.
func (n SCXMLNode) IsParallel() bool { return n.Kind == NodeParallel }

// SCXMLTransition represents a <transition> element.
type SCXMLTransition struct {
	XMLName xml.Name       `xml:"transition"`
	Event   string         `xml:"event,attr,omitempty"`
	Cond    string         `xml:"cond,attr,omitempty"`
	Target  string         `xml:"target,attr,omitempty"`
	Assign  []*SCXMLAssign `xml:"assign,omitempty"`
}

// SCXMLOnEntry wraps executable content on state entry.
type SCXMLOnEntry struct {
	XMLName      xml.Name      `xml:"onentry"`
	SendElements []*SCXMLSend `xml:"send,omitempty"`
}

// UnmarshalXML handles <onentry> with unknown children (raise, datamodel, etc.).
func (e *SCXMLOnEntry) UnmarshalXML(dec *xml.Decoder, start xml.StartElement) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "send":
				se := &SCXMLSend{}
				if err := dec.DecodeElement(se, &t); err != nil {
					return err
				}
				e.SendElements = append(e.SendElements, se)
			default:
				if err := dec.Skip(); err != nil {
					return err
				}
			}
		case xml.EndElement:
			return nil
		}
	}
}

// SCXMLOnExit wraps executable content on state exit.
type SCXMLOnExit struct {
	XMLName xml.Name `xml:"onexit"`
}

// UnmarshalXML handles <onexit> with unknown children.
func (e *SCXMLOnExit) UnmarshalXML(dec *xml.Decoder, start xml.StartElement) error {
	return dec.Skip()
}

// SCXMLInvoke represents an <invoke> element.
type SCXMLInvoke struct {
	XMLName xml.Name `xml:"invoke"`
	ID      string   `xml:"id,attr"`
}

// UnmarshalXML handles <invoke> with unknown children.
func (e *SCXMLInvoke) UnmarshalXML(dec *xml.Decoder, start xml.StartElement) error {
	for _, attr := range start.Attr {
		if attr.Name.Local == "id" {
			e.ID = attr.Value
		}
	}
	return dec.Skip()
}

// SCXMLAssign represents an <assign> element.
type SCXMLAssign struct {
	Location string `xml:"location,attr,omitempty"`
}

// SCXMLSend represents a <send> element.
type SCXMLSend struct {
	Event string `xml:"event,attr,omitempty"`
	Delay string `xml:"delay,attr,omitempty"`
}

// ToSCXML converts a gstate Machine definition into an SCXML document.
func ToSCXML[S ~string, E ~string, C any](m *Machine[S, E, C]) (*SCXMLDocument, error) {
	doc := &SCXMLDocument{
		XMLNS:   "http://www.w3.org/2005/07/scxml",
		Version: "1.0",
		Name:    m.ID,
		Initial: string(m.Initial),
	}

	topLevel := make([]*StateDef[S, E, C], 0)
	for _, s := range m.States {
		if s.Parent == "" {
			topLevel = append(topLevel, s)
		}
	}

	sort.Slice(topLevel, func(i, j int) bool {
		return string(topLevel[i].ID) < string(topLevel[j].ID)
	})

	for _, s := range topLevel {
		node, err := convertStateNode(s, m)
		if err != nil {
			return nil, err
		}
		doc.Children = append(doc.Children, node)
	}

	return doc, nil
}

func convertStateNode[S ~string, E ~string, C any](def *StateDef[S, E, C], m *Machine[S, E, C]) (SCXMLNode, error) {
	if def.Type == Final {
		return convertFinalNode(def, m)
	}

	nodeKind := NodeState
	if def.Type == Parallel {
		nodeKind = NodeParallel
	}

	node := SCXMLNode{
		Kind: nodeKind,
		ID:   string(def.ID),
	}

	if def.Initial != "" {
		node.Initial = string(def.Initial)
	}

	if len(def.Entry) > 0 {
		node.OnEntry = &SCXMLOnEntry{}
	}

	if len(def.Delayed) > 0 {
		if node.OnEntry == nil {
			node.OnEntry = &SCXMLOnEntry{}
		}
		for _, t := range def.Delayed {
			node.OnEntry.SendElements = append(node.OnEntry.SendElements, &SCXMLSend{
				Event: fmt.Sprintf("_delay.%s", def.ID),
				Delay: formatDuration(t.After),
			})
		}
	}

	if len(def.Exit) > 0 {
		node.OnExit = &SCXMLOnExit{}
	}

	// Use eventOrder to preserve declaration order of transitions
	for _, event := range def.eventOrder {
		for _, t := range def.Transitions[event] {
			node.Transitions = append(node.Transitions, convertTransition(t, string(event)))
		}
	}

	for _, t := range def.Always {
		node.Transitions = append(node.Transitions, convertTransition(t, ""))
	}

	for _, t := range def.Delayed {
		eventName := fmt.Sprintf("_delay.%s", def.ID)
		node.Transitions = append(node.Transitions, convertTransition(t, eventName))
	}

	if def.Invoke != nil {
		invokeID := string(def.ID)
		node.Invoke = &SCXMLInvoke{ID: invokeID}
		if def.Invoke.OnDone != "" {
			node.Transitions = append(node.Transitions, &SCXMLTransition{
				Event:  fmt.Sprintf("done.invoke.%s", invokeID),
				Target: string(def.Invoke.OnDone),
			})
		}
		if def.Invoke.OnError != "" {
			node.Transitions = append(node.Transitions, &SCXMLTransition{
				Event:  "error.platform",
				Target: string(def.Invoke.OnError),
			})
		}
	}

	if def.Type == Parallel {
		for _, childDef := range sortedChildren(def) {
			childNode, err := convertStateNode(childDef, m)
			if err != nil {
				return SCXMLNode{}, err
			}
			node.Children = append(node.Children, childNode)
		}
	} else if len(def.States) > 0 {
		if def.History == Shallow || def.History == Deep {
			ht := "shallow"
			if def.History == Deep {
				ht = "deep"
			}
			node.Children = append(node.Children, SCXMLNode{
				Kind:        NodeHistory,
				ID:          string(def.ID) + ".hist",
				HistoryMode: ht,
			})
		}
		for _, childDef := range sortedChildren(def) {
			childNode, err := convertStateNode(childDef, m)
			if err != nil {
				return SCXMLNode{}, err
			}
			node.Children = append(node.Children, childNode)
		}
	}

	return node, nil
}

func convertFinalNode[S ~string, E ~string, C any](def *StateDef[S, E, C], m *Machine[S, E, C]) (SCXMLNode, error) {
	node := SCXMLNode{Kind: NodeFinal, ID: string(def.ID)}
	if len(def.Entry) > 0 {
		node.OnEntry = &SCXMLOnEntry{}
	}
	if len(def.Exit) > 0 {
		node.OnExit = &SCXMLOnExit{}
	}
	return node, nil
}

func convertTransition[S ~string, E ~string, C any](t *TransitionDef[S, E, C], event string) *SCXMLTransition {
	tr := &SCXMLTransition{Event: event, Target: string(t.Target)}
	if t.Guard != nil {
		if t.GuardName != "" {
			tr.Cond = t.GuardName
		} else {
			tr.Cond = "guard"
		}
	}
	if t.Action != nil {
		tr.Assign = []*SCXMLAssign{{Location: t.ActionName}}
	}
	return tr
}

func sortedChildren[S ~string, E ~string, C any](def *StateDef[S, E, C]) []*StateDef[S, E, C] {
	result := make([]*StateDef[S, E, C], 0, len(def.States))
	for _, v := range def.States {
		result = append(result, v)
	}
	sort.Slice(result, func(i, j int) bool {
		return string(result[i].ID) < string(result[j].ID)
	})
	return result
}

func formatDuration(d time.Duration) string {
	return fmt.Sprintf("%dms", d.Milliseconds())
}

// ToSCXMLString converts a Machine to a pretty-printed SCXML string.
func ToSCXMLString[S ~string, E ~string, C any](m *Machine[S, E, C]) (string, error) {
	doc, err := ToSCXML(m)
	if err != nil {
		return "", err
	}
	data, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	return xml.Header + string(data) + "\n", nil
}

// ToSCXMLBytes converts a Machine to SCXML bytes with XML header.
func ToSCXMLBytes[S ~string, E ~string, C any](m *Machine[S, E, C]) ([]byte, error) {
	data, err := ToSCXMLString(m)
	if err != nil {
		return nil, err
	}
	return []byte(data), nil
}
