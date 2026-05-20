package gstate

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"
)

// TestStopWaitsForInvokeGoroutine asserts that when Stop returns, every
// invoke goroutine the actor spawned has fully unwound (including any
// deferred cleanup). On main this fails because Stop only cancels the
// invoke's context but does not wait for the goroutine to exit.
func TestStopWaitsForInvokeGoroutine(t *testing.T) {
	started := make(chan struct{})
	done := make(chan struct{})

	m := New[StateID, EventID, Context]("stop_drain_invoke").
		Initial("active").
		State("active", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Invoke(func(ctx context.Context, _ Context, _ func(func(Context) Context)) error {
				defer close(done)
				close(started)
				<-ctx.Done()
				return ctx.Err()
			}, "ok", "err")
		}).
		State("ok", func(s *StateBuilder[StateID, EventID, Context]) { s.Type(Final) }).
		State("err", func(s *StateBuilder[StateID, EventID, Context]) { s.Type(Final) }).
		Build()

	a := Start(m, Context{})
	<-started // wait for the invoke Src to actually be running

	a.Stop()

	select {
	case <-done:
		// expected: invoke goroutine ran its defers before Stop returned
	default:
		t.Fatal("Stop returned before invoke goroutine completed (defer close(done) did not run)")
	}
}

// TestSendAfterStopIsNoOp asserts that Send and SendCtx called after Stop
// return cleanly without panic and without delivering the event to any
// observer. On main this panics (or relies on defer recover()) because Stop
// closes the mailbox out from under any caller still trying to send.
func TestSendAfterStopIsNoOp(t *testing.T) {
	m := tinyMachine()
	rec := &RecordingObserver[StateID, EventID, Context]{}
	a := Start(m, Context{}, m.WithObserver(rec))

	a.Stop()
	rec.Reset() // discard any events the loop processed before Stop

	// Both should return without panicking.
	a.Send("GO")
	_ = a.SendCtx(context.Background(), "GO")

	if got := len(rec.EventsReceived()); got != 0 {
		t.Fatalf("expected zero OnEventReceived after Stop, got %d", got)
	}
}

// TestConcurrentSendDuringStop spawns many senders that race a Stop call.
// On main this is racy: Stop closes the mailbox, and any goroutine in the
// middle of a `mailbox <- env` panics (currently swallowed by defer recover).
// Under the new design, senders bail cleanly via the stopped channel.
// Run with -race to also catch any data races introduced by the change.
func TestConcurrentSendDuringStop(t *testing.T) {
	m := tinyMachine()
	a := Start(m, Context{})

	const senders = 50
	const eventsPerSender = 200

	var wg sync.WaitGroup
	wg.Add(senders)
	start := make(chan struct{})
	for i := 0; i < senders; i++ {
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < eventsPerSender; j++ {
				a.Send("GO")
			}
		}()
	}
	close(start)

	a.Stop()
	wg.Wait() // every sender must have returned cleanly
}

// TestStopWaitsForInFlightTransitionAction asserts the mu-ordering half of
// the Stop completion contract: an action that is already running when Stop
// is called runs to completion before Stop returns. The action signals it
// has begun, then blocks; the test concurrently calls Stop and then releases
// the action. When Stop returns, the action's deferred cleanup must already
// have run.
func TestStopWaitsForInFlightTransitionAction(t *testing.T) {
	actionStarted := make(chan struct{})
	trigger := make(chan struct{})
	finished := make(chan struct{})

	actionFn := func(c Context) Context {
		defer close(finished)
		close(actionStarted)
		<-trigger
		return c
	}

	m := New[StateID, EventID, Context]("stop_inflight_action").
		Initial("a").
		State("a", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("GO").Assign(actionFn).GoTo("b")
		}).
		State("b", func(_ *StateBuilder[StateID, EventID, Context]) {}).
		Build()

	a := Start(m, Context{})

	a.Send("GO")
	<-actionStarted // loop goroutine is inside handleEvent, holding mu

	stopDone := make(chan struct{})
	go func() {
		a.Stop()
		close(stopDone)
	}()

	// Release the action. handleEvent finishes, releases mu, and Stop can
	// proceed with the rest of its shutdown sequence.
	close(trigger)

	<-stopDone

	select {
	case <-finished:
		// expected: action's defer ran before Stop returned
	default:
		t.Fatal("Stop returned while an in-flight transition action was still running")
	}
}

// TestNoGoroutineLeakAfterStop verifies that Stop tears down every
// goroutine the actor owns (loop + invokes) — not just the ones we
// explicitly instrument in the other tests. goleak's deferred check runs
// at test end and compares against the goroutines present at the start.
func TestNoGoroutineLeakAfterStop(t *testing.T) {
	// Baseline at function entry so the verify only flags goroutines this
	// test introduced. Other tests in this package don't always call Stop
	// on their actors; that's a separate cleanliness issue, not the
	// behavior we're asserting here.
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	started := make(chan struct{})

	m := New[StateID, EventID, Context]("stop_no_leak").
		Initial("active").
		State("active", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Invoke(func(ctx context.Context, _ Context, _ func(func(Context) Context)) error {
				close(started)
				<-ctx.Done()
				return ctx.Err()
			}, "ok", "err")
			// A delayed transition that will never fire because Stop comes
			// well before the duration elapses; exercises the timer path.
			s.After(time.Hour).GoTo("ok")
		}).
		State("ok", func(s *StateBuilder[StateID, EventID, Context]) { s.Type(Final) }).
		State("err", func(s *StateBuilder[StateID, EventID, Context]) { s.Type(Final) }).
		Build()

	a := Start(m, Context{})
	<-started

	a.Stop()
}
