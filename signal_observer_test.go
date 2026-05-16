package gstate

import (
	"sync/atomic"
	"testing"
)

// TestSignalObserverFiresOnEachCallback asserts that every lifecycle
// callback calls the supplied signal function. With a guarded
// transition fired by Send("GO"), the post-Send sequence is exactly
// six callbacks: EventReceived, GuardEvaluated, StateExited,
// ActionExecuted, StateEntered, Transition (see
// TestLifecycleHooksHappyPathOrder). Start itself fires one
// StateEntered for the initial state. Total: 7.
func TestSignalObserverFiresOnEachCallback(t *testing.T) {
	var count atomic.Int64
	sig := SignalObserver[StateID, EventID, Context](func() {
		count.Add(1)
	})
	bar := newKindBarrier(KindTransition, 1)

	m := guardedMachine(true)
	a := Start(m, Context{}, m.WithObserver(
		MultiObserver[StateID, EventID, Context]{sig, bar},
	))
	defer a.Stop()

	a.Send("GO")
	<-bar.done

	if got := count.Load(); got != 7 {
		t.Fatalf("signal fired %d times, want 7 (1 initial StateEntered + 6 post-Send)", got)
	}
}

// TestSignalObserverNilSignalIsNoOp installs a nil-signal observer
// and runs an actor through transitions. No panic, no observable
// effect beyond the other observers in the chain.
func TestSignalObserverNilSignalIsNoOp(t *testing.T) {
	sig := SignalObserver[StateID, EventID, Context](nil)
	bar := newKindBarrier(KindTransition, 1)

	m := guardedMachine(true)
	a := Start(m, Context{}, m.WithObserver(
		MultiObserver[StateID, EventID, Context]{sig, bar},
	))
	defer a.Stop()

	a.Send("GO")
	<-bar.done // would not return if SignalObserver panicked or deadlocked
}

// TestSignalObserverWakesChannelDeterministically demonstrates the
// motivating use case: a buffered channel woken on first lifecycle
// activity. The assertion is the receive itself — no timing, no
// polling.
func TestSignalObserverWakesChannelDeterministically(t *testing.T) {
	ready := make(chan struct{}, 1)
	sig := SignalObserver[StateID, EventID, Context](func() {
		select {
		case ready <- struct{}{}:
		default:
		}
	})

	m := tinyMachine()
	a := Start(m, Context{}, m.WithObserver(sig))
	defer a.Stop()

	// Start itself fires OnStateEntered, so ready is already pending.
	<-ready
}

// TestSignalObserverComposesWithMultiObserver confirms SignalObserver
// works inside MultiObserver alongside a RecordingObserver: the signal
// count matches the recorder's event count.
func TestSignalObserverComposesWithMultiObserver(t *testing.T) {
	var count atomic.Int64
	sig := SignalObserver[StateID, EventID, Context](func() {
		count.Add(1)
	})
	rec := &RecordingObserver[StateID, EventID, Context]{}
	bar := newKindBarrier(KindTransition, 1)

	m := guardedMachine(true)
	a := Start(m, Context{}, m.WithObserver(
		MultiObserver[StateID, EventID, Context]{sig, rec, bar},
	))
	defer a.Stop()

	a.Send("GO")
	<-bar.done

	if got, want := count.Load(), int64(len(rec.Events())); got != want {
		t.Fatalf("signal count %d != recorder event count %d", got, want)
	}
}
