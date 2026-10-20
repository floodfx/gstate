package gstate

import (
	"context"
	"testing"
	"time"
)

func TestDoneWaitsForInvokeAndCompletion(t *testing.T) {
	release := make(chan struct{})
	completed := make(chan struct{})
	m := New[StateID, EventID, Context]("done").Initial("run").State("run", func(s *StateBuilder[StateID, EventID, Context]) {
		s.Invoke(func(ctx context.Context, _ Context, _ func(func(Context) Context)) error {
			<-ctx.Done()
			<-release
			return nil
		}, "", "")
	}).Build()
	obs := ObserverFuncs[StateID, EventID, Context]{InvokeCompletedFunc: func(context.Context, *InvokeEvent[StateID, EventID, Context]) { close(completed) }}
	a := Start(m, Context{}, m.WithObservers(obs))
	done := a.Done()
	stopped := make(chan struct{})
	go func() { a.Stop(); close(stopped) }()
	<-a.stopped
	select {
	case <-a.Done():
		t.Fatal("Done closed before invoke returned")
	default:
	}
	close(release)
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Done did not close")
	}
	<-completed
	<-stopped
	if a.Done() != done {
		t.Fatal("Done channel changed")
	}
}

func TestDoneAutoStopAndHydrate(t *testing.T) {
	m := New[StateID, EventID, Context]("done").Initial("end").State("end", func(s *StateBuilder[StateID, EventID, Context]) { s.Type(Final) }).Build()
	a := Start(m, Context{})
	b := Hydrate(m, a.Snapshot())
	for _, actor := range []*Actor[StateID, EventID, Context]{a, b} {
		select {
		case <-actor.Done():
		case <-time.After(5 * time.Second):
			t.Fatal("automatic completion did not close Done")
		}
		actor.Stop()
	}
}
