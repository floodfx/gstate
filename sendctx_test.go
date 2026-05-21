package gstate

import (
	"context"
	"errors"
	"testing"
	"time"
)

// ctxRecorder captures the context.Context passed to OnEventReceived.
type ctxRecorder struct {
	BaseObserver[StateID, EventID, Context]
	got chan context.Context
}

func (c *ctxRecorder) OnEventReceived(ctx context.Context, _ *EventNotice[StateID, EventID, Context]) {
	select {
	case c.got <- ctx:
	default:
	}
}

func TestSendCtxPropagatesContext(t *testing.T) {
	m := tinyMachine()
	rec := &ctxRecorder{got: make(chan context.Context, 1)}
	a := Start(m, Context{}, m.WithObservers(rec))
	defer a.Stop()

	type key struct{}
	want := context.WithValue(context.Background(), key{}, "hello")
	_ = a.SendCtx(want, "GO")

	select {
	case got := <-rec.got:
		if got.Value(key{}) != "hello" {
			t.Errorf("ctx value not threaded; got %v", got.Value(key{}))
		}
	case <-time.After(time.Second):
		t.Fatal("OnEventReceived never fired")
	}
}

// TestSendCtxReturnsCtxErrWhenCancelled asserts that SendCtx honors a
// cancelled context during enqueue and does not deliver the event. On main
// this is a compile error because SendCtx returns no value.
func TestSendCtxReturnsCtxErrWhenCancelled(t *testing.T) {
	m := tinyMachine()
	rec := &RecordingObserver[StateID, EventID, Context]{}
	a := Start(m, Context{}, m.WithObservers(rec))
	defer a.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := a.SendCtx(ctx, "GO")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SendCtx err = %v, want context.Canceled", err)
	}
	if got := len(rec.EventsReceived()); got != 0 {
		t.Fatalf("expected zero OnEventReceived for cancelled-ctx send, got %d", got)
	}
}

// TestSendCtxReturnsNilOnSuccess confirms the happy path: nil error on
// successful enqueue, and the observer eventually receives the event.
func TestSendCtxReturnsNilOnSuccess(t *testing.T) {
	m := tinyMachine()
	rec := &ctxRecorder{got: make(chan context.Context, 1)}
	a := Start(m, Context{}, m.WithObservers(rec))
	defer a.Stop()

	if err := a.SendCtx(context.Background(), "GO"); err != nil {
		t.Fatalf("SendCtx err = %v, want nil", err)
	}
	select {
	case <-rec.got:
	case <-time.After(time.Second):
		t.Fatal("OnEventReceived never fired for a successful SendCtx")
	}
}

// TestSendCtxReturnsErrActorStoppedAfterStop asserts that SendCtx returns
// the sentinel error after the actor has been stopped, and that the event
// is not delivered.
func TestSendCtxReturnsErrActorStoppedAfterStop(t *testing.T) {
	m := tinyMachine()
	rec := &RecordingObserver[StateID, EventID, Context]{}
	a := Start(m, Context{}, m.WithObservers(rec))

	a.Stop()
	rec.Reset() // discard anything the loop processed before Stop

	err := a.SendCtx(context.Background(), "GO")
	if !errors.Is(err, ErrActorStopped) {
		t.Fatalf("SendCtx err = %v, want ErrActorStopped", err)
	}
	if got := len(rec.EventsReceived()); got != 0 {
		t.Fatalf("expected zero OnEventReceived post-Stop, got %d", got)
	}
}

// TestSendCtxReturnsCtxErrOnDeadline exercises the deadline-elapsed-mid-block
// branch of SendCtx's select.
//
// This test uses a real time.Duration deadline. That is deliberate and
// load-bearing: the deadline-mid-block path of SendCtx's select can only be
// exercised by a context whose deadline actually elapses while we're parked
// on the channel send. A pre-cancelled ctx (covered by
// TestSendCtxReturnsCtxErrWhenCancelled) takes the fast-path before the
// inner select; a deadline-already-expired ctx behaves the same. To hit the
// inner select's <-ctx.Done() case specifically, the ctx must still be live
// at the call site and expire while parked. We assert on the returned error
// (context.DeadlineExceeded), never on wall-clock duration, so the test is
// not flaky under load.
func TestSendCtxReturnsCtxErrOnDeadline(t *testing.T) {
	actionEntered := make(chan struct{})
	release := make(chan struct{})

	// Block the loop goroutine inside an action so the mailbox cannot drain.
	// Once the loop is parked here, every subsequent SendCtx that fills the
	// buffered mailbox will block on the channel send until ctx expires.
	hold := func(c Context) Context {
		close(actionEntered)
		<-release
		return c
	}

	m := New[StateID, EventID, Context]("sendctx_deadline").
		Initial("a").
		State("a", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("HOLD").Assign(hold).GoTo("b")
		}).
		State("b", func(_ *StateBuilder[StateID, EventID, Context]) {}).
		Build()

	const mailboxSize = 4
	a := Start(m, Context{}, m.WithMailboxSize(mailboxSize))
	t.Cleanup(func() {
		close(release) // let the action return so Stop can drain
		a.Stop()
	})

	// Send the event that runs the blocking action; wait until the loop is
	// parked inside it. The loop has now consumed this one event and is
	// stuck; nothing else will be pulled from the mailbox until release.
	a.Send("HOLD")
	<-actionEntered

	// Fill the mailbox with non-blocking sends so the next call will block.
	// Use Background() so these all succeed.
	for i := 0; i < mailboxSize; i++ {
		if err := a.SendCtx(context.Background(), "FILL"); err != nil {
			t.Fatalf("unexpected err filling mailbox: %v", err)
		}
	}

	// Now the mailbox is full and the loop is blocked. A SendCtx with a
	// short deadline must return DeadlineExceeded.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := a.SendCtx(ctx, "WILL_TIMEOUT")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("SendCtx err = %v, want context.DeadlineExceeded", err)
	}
}

// TestSendIgnoresErrSilently preserves the post-#2 contract that Send is a
// no-op after Stop: no panic, no error, just returns. SendCtx now also
// returns ErrActorStopped for the same case, but Send must keep its void
// signature and silently absorb that.
func TestSendIgnoresErrSilently(t *testing.T) {
	m := tinyMachine()
	a := Start(m, Context{})

	a.Stop()

	// Should not panic. No return to assert; the test passes if Send returns.
	a.Send("GO")
}

func TestSendUsesBackgroundContext(t *testing.T) {
	m := tinyMachine()
	rec := &ctxRecorder{got: make(chan context.Context, 1)}
	a := Start(m, Context{}, m.WithObservers(rec))
	defer a.Stop()

	a.Send("GO")

	select {
	case got := <-rec.got:
		if got == nil {
			t.Fatal("ctx is nil")
		}
		// context.Background() returns the canonical empty context.
		if got != context.Background() {
			// Acceptable: some implementations may wrap; ensure no Deadline / Done.
			if _, ok := got.Deadline(); ok {
				t.Errorf("Send produced a context with a deadline: %v", got)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("OnEventReceived never fired")
	}
}
