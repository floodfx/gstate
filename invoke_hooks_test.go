package gstate

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestInvokeStartedAndCompletedSuccess(t *testing.T) {
	rec := &RecordingObserver[StateID, EventID, Context]{}
	m := New[StateID, EventID, Context]("invoke_obs_ok").
		Initial("loading").
		State("loading", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Invoke(func(_ context.Context, _ Context) error {
				time.Sleep(10 * time.Millisecond)
				return nil
			}, "done", "fail")
		}).
		State("done", func(s *StateBuilder[StateID, EventID, Context]) { s.Type(Final) }).
		State("fail", func(s *StateBuilder[StateID, EventID, Context]) { s.Type(Final) }).
		Build()

	a := Start(m, Context{}, WithObserver[StateID, EventID, Context](rec))
	defer a.Stop()

	waitForKind(t, rec, KindInvokeCompleted, time.Second)

	started := rec.InvokeStarted()
	if len(started) != 1 {
		t.Fatalf("InvokeStarted count = %d, want 1", len(started))
	}
	if started[0].State != "loading" {
		t.Errorf("InvokeStarted.State = %q, want loading", started[0].State)
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

	a := Start(m, Context{}, WithObserver[StateID, EventID, Context](rec))
	defer a.Stop()

	waitForKind(t, rec, KindInvokeCompleted, time.Second)

	completed := rec.InvokeCompleted()
	if len(completed) != 1 {
		t.Fatalf("InvokeCompleted count = %d, want 1", len(completed))
	}
	if !errors.Is(completed[0].Error, wantErr) {
		t.Errorf("InvokeCompleted.Error = %v, want %v", completed[0].Error, wantErr)
	}
}

func TestInvokeCompletedOnCancellation(t *testing.T) {
	rec := &RecordingObserver[StateID, EventID, Context]{}
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

	a := Start(m, Context{}, WithObserver[StateID, EventID, Context](rec))
	defer a.Stop()

	waitForKind(t, rec, KindInvokeStarted, time.Second)
	a.Send("CANCEL")
	waitForKind(t, rec, KindInvokeCompleted, time.Second)

	completed := rec.InvokeCompleted()
	if len(completed) != 1 {
		t.Fatalf("InvokeCompleted count = %d, want 1", len(completed))
	}
	if !errors.Is(completed[0].Error, context.Canceled) {
		t.Errorf("InvokeCompleted.Error = %v, want context.Canceled", completed[0].Error)
	}
}
