package gstate

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestInvokeStartedAndCompletedSuccess(t *testing.T) {
	rec := &RecordingObserver[StateID, EventID, Context]{}
	bar := newKindBarrier(KindInvokeCompleted, 1)
	srcStart := make(chan struct{})
	srcRelease := make(chan struct{})
	m := New[StateID, EventID, Context]("invoke_obs_ok").
		Initial("loading").
		State("loading", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Invoke(func(_ context.Context, _ Context) error {
				close(srcStart)
				<-srcRelease
				return nil
			}, "done", "fail")
		}).
		State("done", func(s *StateBuilder[StateID, EventID, Context]) { s.Type(Final) }).
		State("fail", func(s *StateBuilder[StateID, EventID, Context]) { s.Type(Final) }).
		Build()

	a := Start(m, Context{}, m.WithObserver(MultiObserver[StateID, EventID, Context]{rec, bar}))
	defer a.Stop()

	<-srcStart        // Src is running
	close(srcRelease) // let it complete
	<-bar.done        // OnInvokeCompleted fired

	started := rec.InvokeStarted()
	if len(started) != 1 || started[0].State != "loading" {
		t.Fatalf("InvokeStarted = %+v", started)
	}
	completed := rec.InvokeCompleted()
	if len(completed) != 1 {
		t.Fatalf("InvokeCompleted count = %d, want 1", len(completed))
	}
	if completed[0].Error != nil {
		t.Errorf("InvokeCompleted.Error = %v, want nil", completed[0].Error)
	}
	if completed[0].Duration <= 0 {
		t.Errorf("InvokeCompleted.Duration = %v, want > 0", completed[0].Duration)
	}
}

func TestInvokeCompletedOnError(t *testing.T) {
	rec := &RecordingObserver[StateID, EventID, Context]{}
	bar := newKindBarrier(KindInvokeCompleted, 1)
	wantErr := errors.New("boom")
	m := New[StateID, EventID, Context]("invoke_obs_err").
		Initial("loading").
		State("loading", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Invoke(func(_ context.Context, _ Context) error {
				return wantErr
			}, "done", "fail")
		}).
		State("done", func(s *StateBuilder[StateID, EventID, Context]) { s.Type(Final) }).
		State("fail", func(s *StateBuilder[StateID, EventID, Context]) { s.Type(Final) }).
		Build()

	a := Start(m, Context{}, m.WithObserver(MultiObserver[StateID, EventID, Context]{rec, bar}))
	defer a.Stop()

	<-bar.done

	completed := rec.InvokeCompleted()
	if len(completed) != 1 {
		t.Fatalf("InvokeCompleted count = %d, want 1", len(completed))
	}
	if !errors.Is(completed[0].Error, wantErr) {
		t.Errorf("InvokeCompleted.Error = %v, want %v", completed[0].Error, wantErr)
	}
}

// invokeCtxCapture records the ctx passed to each invoke hook into per-hook
// channels for deterministic synchronisation.
type invokeCtxCapture struct {
	NopObserver[StateID, EventID, Context]
	started   chan context.Context
	completed chan context.Context
}

func (c *invokeCtxCapture) OnInvokeStarted(ctx context.Context, _ InvokeEvent[StateID, EventID, Context]) {
	select {
	case c.started <- ctx:
	default:
	}
}

func (c *invokeCtxCapture) OnInvokeCompleted(ctx context.Context, _ InvokeEvent[StateID, EventID, Context]) {
	select {
	case c.completed <- ctx:
	default:
	}
}

func TestInvokeHooksPropagateSendCtx(t *testing.T) {
	cap := &invokeCtxCapture{
		started:   make(chan context.Context, 1),
		completed: make(chan context.Context, 1),
	}
	m := New[StateID, EventID, Context]("invoke_ctx_trace").
		Initial("idle").
		State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("GO").GoTo("loading")
		}).
		State("loading", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Invoke(func(_ context.Context, _ Context) error {
				return nil
			}, "done", "fail")
		}).
		State("done", func(s *StateBuilder[StateID, EventID, Context]) { s.Type(Final) }).
		State("fail", func(s *StateBuilder[StateID, EventID, Context]) { s.Type(Final) }).
		Build()

	a := Start(m, Context{}, m.WithObserver(cap))
	defer a.Stop()

	type key struct{}
	parent := context.WithValue(context.Background(), key{}, "trace-xyz")
	a.SendCtx(parent, "GO")

	for _, ch := range []chan context.Context{cap.started, cap.completed} {
		select {
		case ctx := <-ch:
			if got := ctx.Value(key{}); got != "trace-xyz" {
				t.Errorf("invoke hook ctx missing trace value; got %v", got)
			}
		case <-time.After(time.Second):
			t.Fatal("invoke hook never fired") // hard timeout floor, not a primary sync
		}
	}
}

func TestInvokeCompletedOnCancellation(t *testing.T) {
	rec := &RecordingObserver[StateID, EventID, Context]{}
	startedBar := newKindBarrier(KindInvokeStarted, 1)
	completedBar := newKindBarrier(KindInvokeCompleted, 1)
	m := New[StateID, EventID, Context]("invoke_obs_cancel").
		Initial("loading").
		State("loading", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Invoke(func(ctx context.Context, _ Context) error {
				<-ctx.Done()
				return ctx.Err()
			}, "done", "fail")
			s.On("CANCEL").GoTo("done")
		}).
		State("done", func(s *StateBuilder[StateID, EventID, Context]) { s.Type(Final) }).
		State("fail", func(s *StateBuilder[StateID, EventID, Context]) { s.Type(Final) }).
		Build()

	a := Start(m, Context{}, m.WithObserver(MultiObserver[StateID, EventID, Context]{rec, startedBar, completedBar}))
	defer a.Stop()

	<-startedBar.done
	a.Send("CANCEL")
	<-completedBar.done

	completed := rec.InvokeCompleted()
	if len(completed) != 1 {
		t.Fatalf("InvokeCompleted count = %d, want 1", len(completed))
	}
	if !errors.Is(completed[0].Error, context.Canceled) {
		t.Errorf("InvokeCompleted.Error = %v, want context.Canceled", completed[0].Error)
	}
}

func TestInvokeDoneCanAlwaysTransitionIntoAnotherInvoke(t *testing.T) {
	secondStarted := make(chan error, 1)
	m := New[StateID, EventID, Context]("invoke_always_invoke").
		Initial("first").
		State("first", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Invoke(func(_ context.Context, _ Context) error {
				return nil
			}, "routing", "failed")
		}).
		State("routing", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Always().GoTo("second")
		}).
		State("second", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Invoke(func(ctx context.Context, _ Context) error {
				secondStarted <- ctx.Err()
				return nil
			}, "done", "failed")
		}).
		State("done", func(s *StateBuilder[StateID, EventID, Context]) { s.Type(Final) }).
		State("failed", func(s *StateBuilder[StateID, EventID, Context]) { s.Type(Final) }).
		Build()

	a := Start(m, Context{})
	defer a.Stop()

	select {
	case err := <-secondStarted:
		if err != nil {
			t.Fatalf("second invoke started with canceled context: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("second invoke never started")
	}
}
