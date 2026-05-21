package gstate

import (
	"testing"
)

func tinyMachine() *Machine[StateID, EventID, Context] {
	return New[StateID, EventID, Context]("opts").
		Initial("a").
		State("a", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("GO").GoTo("b")
		}).
		State("b", func(_ *StateBuilder[StateID, EventID, Context]) {}).
		Build()
}

func TestStartGeneratesActorID(t *testing.T) {
	m := tinyMachine()
	a := Start(m, Context{})
	defer a.Stop()
	if a.ID() == "" {
		t.Fatal("expected non-empty ActorID, got empty")
	}
}

func TestStartActorIDsAreDistinct(t *testing.T) {
	m := tinyMachine()
	a1 := Start(m, Context{})
	defer a1.Stop()
	a2 := Start(m, Context{})
	defer a2.Stop()
	if a1.ID() == a2.ID() {
		t.Errorf("expected distinct IDs, both = %q", a1.ID())
	}
}

func TestWithActorIDOverride(t *testing.T) {
	m := tinyMachine()
	a := Start(m, Context{}, m.WithActorID("custom-id"))
	defer a.Stop()
	if a.ID() != "custom-id" {
		t.Errorf("ID() = %q, want %q", a.ID(), "custom-id")
	}
}

func TestWithMailboxSize(t *testing.T) {
	m := tinyMachine()
	a := Start(m, Context{}, m.WithMailboxSize(7))
	defer a.Stop()
	if got := cap(a.mailbox); got != 7 {
		t.Errorf("mailbox cap = %d, want 7", got)
	}
}

func TestWithObserverInstalled(t *testing.T) {
	m := tinyMachine()
	rec := &RecordingObserver[StateID, EventID, Context]{}
	a := Start(m, Context{}, m.WithObservers(rec))
	defer a.Stop()
	if len(a.transitionObs) == 0 || a.transitionObs[0] != rec {
		t.Errorf("observer not installed")
	}
}

func TestDefaultObserversAreEmpty(t *testing.T) {
	m := tinyMachine()
	a := Start(m, Context{})
	defer a.Stop()
	if len(a.transitionObs) != 0 {
		t.Errorf("expected no transition observers, got %d", len(a.transitionObs))
	}
}
