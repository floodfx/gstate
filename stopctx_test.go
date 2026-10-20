package gstate

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A real deadline exercises StopCtx's timeout while an invoke refuses to return;
// a channel cannot substitute for context.DeadlineExceeded.
func TestStopCtxInvokeDeadline(t *testing.T) {
	release := make(chan struct{})
	cancelled := make(chan struct{})
	callback := make(chan func(func(Context) Context), 1)
	rec := &RecordingObserver[StateID, EventID, Context]{}
	m := New[StateID, EventID, Context]("stopctx").Initial("run").State("run", func(s *StateBuilder[StateID, EventID, Context]) {
		s.Invoke(func(ctx context.Context, _ Context, mutate func(func(Context) Context)) error {
			callback <- mutate
			<-ctx.Done()
			close(cancelled)
			<-release
			return nil
		}, "", "")
	}).Build()
	a := Start(m, Context{}, m.WithObservers(rec))
	defer func() { close(release); a.Stop() }()
	mutate := <-callback
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := a.StopCtx(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("StopCtx = %v", err)
	}
	<-cancelled
	select {
	case <-a.Done():
		t.Fatal("Done closed while invoke still running")
	default:
	}
	rec.Reset()
	mutate(func(d Context) Context { d.Count = 99; return d })
	if a.Data().Count != 0 || len(rec.Actions()) != 0 || len(rec.Transitions()) != 0 {
		t.Fatal("post-stop mutate was not silent")
	}
	// Every cancelled waiter returns without cancelling shutdown itself.
	cancelledCtx, cancelWait := context.WithCancel(context.Background())
	cancelWait()
	for range 3 {
		if err := a.StopCtx(cancelledCtx); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled waiter = %v", err)
		}
	}
}

// A real deadline must expire while an action owns the actor lock. A channel
// cannot exercise the deadline branch or verify that StopCtx bounds lock waiting.
func TestStopCtxActionDeadline(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	m := New[StateID, EventID, Context]("stopctx-lock").Initial("run").State("run", func(s *StateBuilder[StateID, EventID, Context]) {
		s.On("BLOCK").Assign(func(d Context) Context { close(entered); <-release; return d })
	}).Build()
	a := Start(m, Context{})
	defer func() { close(release); a.Stop() }()
	a.Send("BLOCK")
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- a.StopCtx(ctx) }()
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("StopCtx = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("StopCtx blocked on actor lock")
	}
}

func TestStopCtxNormalAndAlreadyDone(t *testing.T) {
	m := New[StateID, EventID, Context]("stopctx-normal").Initial("run").State("run", func(s *StateBuilder[StateID, EventID, Context]) {}).Build()
	a := Start(m, Context{})
	defer a.Stop()
	if err := a.StopCtx(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.Done():
	default:
		t.Fatal("successful StopCtx returned before Done")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.StopCtx(ctx); err != nil {
		t.Fatalf("completed actor = %v", err)
	}
}
