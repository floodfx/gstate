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

func TestHydrateAcceptsObserverOption(t *testing.T) {
	m := tinyMachine()
	a := Start(m, Context{})
	snap := a.Snapshot()
	a.Stop()

	rec := &RecordingObserver[StateID, EventID, Context]{}
	revived := Hydrate(m, snap, m.WithObservers(rec))
	defer revived.Stop()
	if len(revived.transitionObs) == 0 || revived.transitionObs[0] != rec {
		t.Errorf("observer not installed via Hydrate")
	}
}

func TestHydrateDoesNotFireOnStateEntered(t *testing.T) {
	// Hydrate restarts services but skips Entry actions and the
	// OnStateEntered hook. This lets persisted workflows resume without
	// double-firing side effects. Document and lock the contract.
	//
	// The machine has no async services, so Hydrate has no goroutines that
	// could fire observers asynchronously. Any observer call would happen
	// inline during Hydrate; checking immediately after is deterministic.
	m := tinyMachine()
	original := Start(m, Context{})
	snap := original.Snapshot()
	original.Stop()

	rec := &RecordingObserver[StateID, EventID, Context]{}
	revived := Hydrate(m, snap, m.WithObservers(rec))
	defer revived.Stop()

	if got := rec.StateEntered(); len(got) != 0 {
		t.Errorf("Hydrate must not emit OnStateEntered for restored states; got %+v", got)
	}
	if got := rec.Transitions(); len(got) != 0 {
		t.Errorf("Hydrate must not emit OnTransition for restored states; got %+v", got)
	}
}

func TestHydrateWithActorIDOverridesSnapshot(t *testing.T) {
	m := tinyMachine()
	a := Start(m, Context{})
	snap := a.Snapshot()
	a.Stop()

	override := ActorID("forced-id")
	revived := Hydrate(m, snap, m.WithActorID(override))
	defer revived.Stop()
	if revived.ID() != override {
		t.Errorf("Hydrate WithActorID = %q, want %q", revived.ID(), override)
	}
}
