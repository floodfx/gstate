package gstate

import (
	"sync/atomic"
	"testing"
)

// TestSignalObserverFiresOnEachCallback asserts that every lifecycle
// callback calls the supplied signal function. A RecordingObserver registered
// alongside it supplies the ground-truth event count so this
// test does not pin a specific callback inventory — that is the job of
// the lifecycle hook tests (TestLifecycleHooksHappyPathOrder etc.).
func TestSignalObserverFiresOnEachCallback(t *testing.T) {
	var count atomic.Int64
	sig := SignalObserver[StateID, EventID, Context](func() {
		count.Add(1)
	})
	rec := &RecordingObserver[StateID, EventID, Context]{}
	bar := newKindBarrier(KindTransition, 1)

	m := guardedMachine(true)
	a := Start(m, Context{}, m.WithObservers(sig, rec, bar))
	defer a.Stop()

	a.Send("GO")
	<-bar.done

	if got, want := count.Load(), int64(len(rec.Events())); got != want {
		t.Fatalf("signal fired %d times, recorder saw %d events", got, want)
	}
}

// TestSignalObserverNilSignalIsNoOp installs a nil-signal observer
// and runs an actor through transitions. No panic, no observable
// effect beyond the other observers in the chain.
func TestSignalObserverNilSignalIsNoOp(t *testing.T) {
	sig := SignalObserver[StateID, EventID, Context](nil)
	bar := newKindBarrier(KindTransition, 1)

	m := guardedMachine(true)
	a := Start(m, Context{}, m.WithObservers(sig, bar))
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
	a := Start(m, Context{}, m.WithObservers(sig))
	defer a.Stop()

	// Start itself fires OnStateEntered, so ready is already pending.
	<-ready
}
