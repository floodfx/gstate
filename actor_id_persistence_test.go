package gstate

import "testing"

func TestSnapshotCapturesActorID(t *testing.T) {
	m := tinyMachine()
	a := Start(m, Context{})
	defer a.Stop()
	snap := a.Snapshot()
	if snap.ActorID != a.ID() {
		t.Errorf("Snapshot.ActorID = %q, want %q", snap.ActorID, a.ID())
	}
	if snap.ActorID == "" {
		t.Fatal("Snapshot.ActorID is empty")
	}
}

func TestHydrateRestoresActorID(t *testing.T) {
	m := tinyMachine()
	a := Start(m, Context{})
	original := a.ID()
	snap := a.Snapshot()
	a.Stop()

	revived := Hydrate(m, snap)
	defer revived.Stop()
	if revived.ID() != original {
		t.Errorf("hydrated ID = %q, want %q", revived.ID(), original)
	}
}

func TestHydrateMintsIDWhenSnapshotEmpty(t *testing.T) {
	m := tinyMachine()
	// Simulate a legacy snapshot with no ActorID field populated.
	legacy := Snapshot[StateID, Context]{
		Active:  []StateID{"a"},
		History: map[StateID]StateID{},
		Context: Context{},
	}
	a := Hydrate(m, legacy)
	defer a.Stop()
	if a.ID() == "" {
		t.Error("expected fresh ActorID when snapshot ActorID is empty")
	}
}

func TestHydrateAcceptsObserverOption(t *testing.T) {
	m := tinyMachine()
	a := Start(m, Context{})
	snap := a.Snapshot()
	a.Stop()

	rec := &RecordingObserver[StateID, EventID, Context]{}
	revived := Hydrate(m, snap, WithObserver[StateID, EventID, Context](rec))
	defer revived.Stop()
	if revived.observer != rec {
		t.Errorf("observer not installed via Hydrate; got %T", revived.observer)
	}
}

func TestHydrateWithActorIDOverridesSnapshot(t *testing.T) {
	m := tinyMachine()
	a := Start(m, Context{})
	snap := a.Snapshot()
	a.Stop()

	override := ActorID("forced-id")
	revived := Hydrate(m, snap, WithActorID[StateID, EventID, Context](override))
	defer revived.Stop()
	if revived.ID() != override {
		t.Errorf("Hydrate WithActorID = %q, want %q", revived.ID(), override)
	}
}
