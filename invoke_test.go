package gstate

import (
	"context"
	"errors"
	"testing"
)

func TestActorInvoke(t *testing.T) {
	release := make(chan struct{})
	done := make(chan struct{}, 1)
	m := New[StateID, EventID, Context]("invoke").
		Initial("loading").
		State("loading", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Invoke(func(ctx context.Context, _ Context, _ func(func(Context) Context)) error {
				<-release
				return nil
			}, "success", "failure")
		}).
		State("success", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Type(Final)
		}).
		State("failure", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Type(Final)
		}).
		Build()

	actor := Start(m, Context{}, m.WithObservers(ObserverFuncs[StateID, EventID, Context]{TransitionFunc: func(context.Context, *TransitionEvent[StateID, EventID, Context]) { done <- struct{}{} }}))
	defer actor.Stop()

	if actor.State() != "loading" {
		t.Errorf("Expected state loading, got %s", actor.State())
	}

	close(release)
	<-done
	if actor.State() != "success" {
		t.Errorf("Expected state success after invocation, got %s", actor.State())
	}
}

func TestActorInvokeError(t *testing.T) {
	release := make(chan struct{})
	done := make(chan struct{}, 1)
	m := New[StateID, EventID, Context]("invoke_error").
		Initial("loading").
		State("loading", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Invoke(func(ctx context.Context, _ Context, _ func(func(Context) Context)) error {
				<-release
				return errors.New("fail")
			}, "success", "failure")
		}).
		State("success", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Type(Final)
		}).
		State("failure", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Type(Final)
		}).
		Build()

	actor := Start(m, Context{}, m.WithObservers(ObserverFuncs[StateID, EventID, Context]{TransitionFunc: func(context.Context, *TransitionEvent[StateID, EventID, Context]) { done <- struct{}{} }}))
	defer actor.Stop()

	close(release)
	<-done
	if actor.State() != "failure" {
		t.Errorf("Expected state failure after error, got %s", actor.State())
	}
}
