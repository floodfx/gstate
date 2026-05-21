package gstate

import (
	"context"
	"testing"
)

// TestMachineOptionsInferTypeParams confirms that the machine-method form of
// option helpers (m.WithMailboxSize, m.WithObservers, m.WithActorID) works
// without explicit [S, E, C] annotations at the call site. The test
// expresses this by *being* the call site: if this file compiles, inference
// works.
func TestMachineOptionsInferTypeParams(t *testing.T) {
	m := tinyMachine()
	rec := &RecordingObserver[StateID, EventID, Context]{}

	a := Start(m, Context{},
		m.WithMailboxSize(7),
		m.WithObservers(rec),
		m.WithActorID("worker-id"),
	)
	defer a.Stop()

	if a.ID() != "worker-id" {
		t.Errorf("ActorID = %q, want worker-id", a.ID())
	}
	if cap(a.mailbox) != 7 {
		t.Errorf("mailbox cap = %d, want 7", cap(a.mailbox))
	}
	if len(a.transitionObs) == 0 || a.transitionObs[0] != rec {
		t.Errorf("observer not installed via m.WithObservers")
	}
}

// TestMachineOptionsBlockReceivedSignal mirrors a typical end-to-end use of
// the inferred option form: install a barrier observer on Start, drive an
// event, block on the observer's channel for completion. The block
// proves the option made it into the running actor — no time.Sleep.
func TestMachineOptionsBlockReceivedSignal(t *testing.T) {
	m := tinyMachine()
	barrier := &transitionBarrier{ch: make(chan struct{}, 1)}

	a := Start(m, Context{}, m.WithObservers(barrier))
	defer a.Stop()
	a.Send("GO")

	<-barrier.ch // deterministic: blocks until OnTransition fires
}

type transitionBarrier struct {
	BaseObserver[StateID, EventID, Context]
	ch chan struct{}
}

func (b *transitionBarrier) OnTransition(context.Context, *TransitionEvent[StateID, EventID, Context]) {
	select {
	case b.ch <- struct{}{}:
	default:
	}
}
