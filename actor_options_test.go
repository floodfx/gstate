package gstate

import (
	"context"
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
	a := Start(m, Context{}, WithActorID[StateID, EventID, Context]("custom-id"))
	defer a.Stop()
	if a.ID() != "custom-id" {
		t.Errorf("ID() = %q, want %q", a.ID(), "custom-id")
	}
}

func TestWithMailboxSize(t *testing.T) {
	m := tinyMachine()
	a := Start(m, Context{}, WithMailboxSize[StateID, EventID, Context](7))
	defer a.Stop()
	if got := cap(a.mailbox); got != 7 {
		t.Errorf("mailbox cap = %d, want 7", got)
	}
}

func TestWithObserverInstalled(t *testing.T) {
	m := tinyMachine()
	rec := &RecordingObserver[StateID, EventID, Context]{}
	a := Start(m, Context{}, WithObserver[StateID, EventID, Context](rec))
	defer a.Stop()
	if a.observer != rec {
		t.Errorf("observer not installed; got %T", a.observer)
	}
}

func TestDefaultObserverIsNop(t *testing.T) {
	m := tinyMachine()
	a := Start(m, Context{})
	defer a.Stop()
	if a.observer == nil {
		t.Fatal("default observer must not be nil")
	}
	// NopObserver is a value type; just confirm method calls don't panic.
	a.observer.OnTransition(context.Background(), TransitionEvent[StateID, EventID, Context]{})
}
