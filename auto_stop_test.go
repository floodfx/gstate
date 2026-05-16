package gstate

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/goleak"
)

// finalMachine builds a small machine with an event-triggered transition
// from "active" to top-level Final "done".
func finalMachine() *Machine[StateID, EventID, Context] {
	return New[StateID, EventID, Context]("final_atomic").
		Initial("active").
		State("active", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("FINISH").GoTo("done")
		}).
		State("done", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Type(Final)
		}).
		Build()
}

// TestAutoStopOnAtomicTopLevelFinal asserts that when the actor lands
// on a top-level state whose Type is Final, it stops itself.
func TestAutoStopOnAtomicTopLevelFinal(t *testing.T) {
	a := Start(finalMachine(), Context{})
	defer a.Stop() // idempotent safety net

	if err := a.SendCtx(context.Background(), "FINISH"); err != nil {
		t.Fatalf("SendCtx(FINISH) err = %v, want nil", err)
	}

	// Wait deterministically for the auto-stop goroutine to signal
	// shutdown via close(a.stopped). Same-package test, so direct
	// access is fine.
	<-a.stopped

	// Once stopped is closed, SendCtx must return ErrActorStopped.
	if err := a.SendCtx(context.Background(), "anything"); !errors.Is(err, ErrActorStopped) {
		t.Fatalf("SendCtx after auto-stop: err = %v, want ErrActorStopped", err)
	}

	// Snapshot must still work post-auto-stop.
	snap := a.Snapshot()
	foundDone := false
	for _, sID := range snap.Active {
		if sID == "done" {
			foundDone = true
		}
	}
	if !foundDone {
		t.Fatalf("snapshot Active = %v, expected to contain 'done'", snap.Active)
	}
}

// TestAutoStopOnCompoundRootReachingFinalChild covers the case where
// the top-level state is compound and the active child of that
// compound state is Final. Per SCXML "done" semantics, the compound
// is itself done.
func TestAutoStopOnCompoundRootReachingFinalChild(t *testing.T) {
	m := New[StateID, EventID, Context]("compound_final").
		Initial("running").
		State("running", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Initial("active")
			s.State("active", func(s *StateBuilder[StateID, EventID, Context]) {
				s.On("FINISH").GoTo("done")
			})
			s.State("done", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Type(Final)
			})
		}).
		Build()

	a := Start(m, Context{})
	defer a.Stop()

	if err := a.SendCtx(context.Background(), "FINISH"); err != nil {
		t.Fatalf("SendCtx err = %v, want nil", err)
	}
	<-a.stopped

	if err := a.SendCtx(context.Background(), "x"); !errors.Is(err, ErrActorStopped) {
		t.Fatalf("post-auto-stop SendCtx err = %v, want ErrActorStopped", err)
	}
}

// TestAutoStopOnNestedFinal covers a three-level deep compound:
// root → running → complete(Final). Auto-stop recursion bubbles up.
func TestAutoStopOnNestedFinal(t *testing.T) {
	m := New[StateID, EventID, Context]("nested_final").
		Initial("root").
		State("root", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Initial("running")
			s.State("running", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Initial("active")
				s.State("active", func(s *StateBuilder[StateID, EventID, Context]) {
					s.On("FINISH").GoTo("complete")
				})
				s.State("complete", func(s *StateBuilder[StateID, EventID, Context]) {
					s.Type(Final)
				})
			})
		}).
		Build()

	a := Start(m, Context{})
	defer a.Stop()

	if err := a.SendCtx(context.Background(), "FINISH"); err != nil {
		t.Fatalf("SendCtx err = %v, want nil", err)
	}
	<-a.stopped
}

// TestAutoStopOnParallelAllRegionsDone covers a parallel top-level
// state where every region reaches a Final descendant. Auto-stop must
// fire when the *last* region completes.
func TestAutoStopOnParallelAllRegionsDone(t *testing.T) {
	m := New[StateID, EventID, Context]("parallel_done").
		Initial("team").
		State("team", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Type(Parallel)
			s.State("regionA", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Initial("a_active")
				s.State("a_active", func(s *StateBuilder[StateID, EventID, Context]) {
					s.On("FINISH_A").GoTo("a_done")
				})
				s.State("a_done", func(s *StateBuilder[StateID, EventID, Context]) {
					s.Type(Final)
				})
			})
			s.State("regionB", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Initial("b_active")
				s.State("b_active", func(s *StateBuilder[StateID, EventID, Context]) {
					s.On("FINISH_B").GoTo("b_done")
				})
				s.State("b_done", func(s *StateBuilder[StateID, EventID, Context]) {
					s.Type(Final)
				})
			})
		}).
		Build()

	a := Start(m, Context{})
	defer a.Stop()

	// Finish region A; actor should NOT yet be stopped (region B is
	// still in a_active... err, b_active).
	if err := a.SendCtx(context.Background(), "FINISH_A"); err != nil {
		t.Fatalf("SendCtx(FINISH_A) err = %v, want nil", err)
	}

	// Drive region B to its Final. After this, both regions are done,
	// so auto-stop must fire.
	if err := a.SendCtx(context.Background(), "FINISH_B"); err != nil {
		t.Fatalf("SendCtx(FINISH_B) err = %v, want nil", err)
	}
	<-a.stopped

	if err := a.SendCtx(context.Background(), "x"); !errors.Is(err, ErrActorStopped) {
		t.Fatalf("post-auto-stop SendCtx err = %v, want ErrActorStopped", err)
	}
}

// TestAutoStopDoesNotFireForParallelPartialDone covers the same
// parallel machine: only region A finishes; the actor must stay alive.
//
// Strategy: send FINISH_A, then send a sentinel NOOP that has no
// matching transition (fires OnEventDropped). Wait deterministically
// for the NOOP's OnEventDropped via a kindBarrier. If auto-stop
// wrongly fires after FINISH_A, the loop exits via the priority
// stopped-check before processing NOOP, and the barrier never
// resolves (test times out, surfacing the bug).
func TestAutoStopDoesNotFireForParallelPartialDone(t *testing.T) {
	m := New[StateID, EventID, Context]("parallel_partial").
		Initial("team").
		State("team", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Type(Parallel)
			s.State("regionA", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Initial("a_active")
				s.State("a_active", func(s *StateBuilder[StateID, EventID, Context]) {
					s.On("FINISH_A").GoTo("a_done")
				})
				s.State("a_done", func(s *StateBuilder[StateID, EventID, Context]) {
					s.Type(Final)
				})
			})
			s.State("regionB", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Initial("b_active")
				s.State("b_active", func(_ *StateBuilder[StateID, EventID, Context]) {})
			})
		}).
		Build()

	dropBar := newKindBarrier(KindEventDropped, 1)
	a := Start(m, Context{}, m.WithObserver(dropBar))
	defer a.Stop()

	// FINISH_A drives regionA to Final; regionB still in b_active.
	if err := a.SendCtx(context.Background(), "FINISH_A"); err != nil {
		t.Fatalf("SendCtx(FINISH_A) err = %v, want nil", err)
	}
	// NOOP has no matching transition; it gets dropped after the loop
	// processes FINISH_A successfully.
	if err := a.SendCtx(context.Background(), "NOOP"); err != nil {
		t.Fatalf("SendCtx(NOOP) err = %v, want nil", err)
	}

	<-dropBar.done // proves the loop is alive past FINISH_A

	// Final assertion: stopped channel is still open.
	select {
	case <-a.stopped:
		t.Fatal("auto-stop fired with only one parallel region in Final")
	default:
		// Expected: actor still alive.
	}
}

// TestAutoStopDoesNotFireForCompoundWithNonFinalActiveChild builds a
// compound root with one Final child and one Atomic child; the Atomic
// child is the Initial. Auto-stop must NOT fire — only the Atomic
// child is active, not the Final sibling.
func TestAutoStopDoesNotFireForCompoundWithNonFinalActiveChild(t *testing.T) {
	m := New[StateID, EventID, Context]("compound_atomic").
		Initial("running").
		State("running", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Initial("active") // active is Atomic, not Final
			s.State("active", func(s *StateBuilder[StateID, EventID, Context]) {
				s.On("FINISH").GoTo("done")
			})
			s.State("done", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Type(Final)
			})
		}).
		Build()

	// Send a NOOP and confirm it's dropped — proves the loop is alive
	// at Start (no auto-stop happened during initial-chain entry).
	dropBar := newKindBarrier(KindEventDropped, 1)
	a := Start(m, Context{}, m.WithObserver(dropBar))
	defer a.Stop()

	if err := a.SendCtx(context.Background(), "NOOP"); err != nil {
		t.Fatalf("SendCtx err = %v, want nil", err)
	}
	<-dropBar.done

	select {
	case <-a.stopped:
		t.Fatal("auto-stop fired even though the active child is not Final")
	default:
	}
}

// TestAutoStopDoesNotFireForMachineWithoutFinal documents the
// "no Final = no auto-stop" property explicitly. tinyMachine has
// states a, b with no Final anywhere; the actor must stay alive
// across transitions.
func TestAutoStopDoesNotFireForMachineWithoutFinal(t *testing.T) {
	dropBar := newKindBarrier(KindEventDropped, 1)
	m := tinyMachine()
	a := Start(m, Context{}, m.WithObserver(dropBar))
	defer a.Stop()

	// Transition a -> b, then NOOP (no transition matches in b).
	if err := a.SendCtx(context.Background(), "GO"); err != nil {
		t.Fatalf("SendCtx(GO) err = %v, want nil", err)
	}
	if err := a.SendCtx(context.Background(), "NOOP"); err != nil {
		t.Fatalf("SendCtx(NOOP) err = %v, want nil", err)
	}
	<-dropBar.done

	select {
	case <-a.stopped:
		t.Fatal("auto-stop fired on a machine with no Final state")
	default:
	}
}

// TestAutoStopOnInitialChain builds a machine whose Initial points
// directly at a top-level Final. Start should return an actor that
// auto-stops immediately (no event needs to be sent).
func TestAutoStopOnInitialChain(t *testing.T) {
	m := New[StateID, EventID, Context]("initial_final").
		Initial("done").
		State("done", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Type(Final)
		}).
		Build()

	a := Start(m, Context{})
	defer a.Stop()

	<-a.stopped // auto-stop must have fired from inside Start

	if err := a.SendCtx(context.Background(), "x"); !errors.Is(err, ErrActorStopped) {
		t.Fatalf("post-auto-stop SendCtx err = %v, want ErrActorStopped", err)
	}
}

// TestAutoStopNoGoroutineLeak verifies that auto-stop's spawned Stop
// goroutine doesn't leak. The baseline (IgnoreCurrent) ignores
// goroutines from prior tests that may not have cleaned up — we only
// care about goroutines introduced by this test.
func TestAutoStopNoGoroutineLeak(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	a := Start(finalMachine(), Context{})
	if err := a.SendCtx(context.Background(), "FINISH"); err != nil {
		t.Fatalf("SendCtx err = %v", err)
	}
	<-a.stopped

	// Stop is idempotent; this also blocks on wg.Wait, ensuring all
	// auto-stop-spawned goroutines have exited before goleak checks.
	a.Stop()
}

// TestAutoStopObserverSeesTerminalState verifies that the observer's
// OnStateEntered for the Final state fires before the actor stops.
// OnStateEntered runs inside executeTransition (under a.mu); auto-stop
// is queued AFTER handleEvent returns, so the observer always sees
// the terminal state.
func TestAutoStopObserverSeesTerminalState(t *testing.T) {
	rec := &RecordingObserver[StateID, EventID, Context]{}
	a := Start(finalMachine(), Context{}, finalMachine().WithObserver(rec))
	defer a.Stop()

	if err := a.SendCtx(context.Background(), "FINISH"); err != nil {
		t.Fatalf("SendCtx err = %v", err)
	}
	<-a.stopped

	// Find the OnStateEntered for "done".
	sawDone := false
	for _, ev := range rec.StateEntered() {
		if ev.State == "done" {
			sawDone = true
			break
		}
	}
	if !sawDone {
		t.Fatalf("observer did not see OnStateEntered for 'done' (entered: %+v)", rec.StateEntered())
	}
}

// TestAutoStopOnHydratedFinal hydrates a snapshot whose Active set is
// already in a top-level Final. The resulting actor must auto-stop.
func TestAutoStopOnHydratedFinal(t *testing.T) {
	m := finalMachine() // active / done(Final)
	snap := Snapshot[StateID, Context]{
		Active:  []StateID{"done"},
		History: map[StateID]StateID{},
		Context: Context{},
		ActorID: "hydrated-final",
	}

	a := Hydrate(m, snap)
	defer a.Stop()

	<-a.stopped // auto-stop must have fired from inside Hydrate

	if err := a.SendCtx(context.Background(), "x"); !errors.Is(err, ErrActorStopped) {
		t.Fatalf("post-auto-stop SendCtx err = %v, want ErrActorStopped", err)
	}
}
