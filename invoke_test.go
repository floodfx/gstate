package gstate

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestActorInvoke(t *testing.T) {
	m := New[StateID, EventID, Context]("invoke").
		Initial("loading").
		State("loading", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Invoke(func(ctx context.Context, c Context) error {
				time.Sleep(20 * time.Millisecond)
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

	actor := Start(m, Context{})
	
	if actor.State() != "loading" {
		t.Errorf("Expected state loading, got %s", actor.State())
	}

	time.Sleep(50 * time.Millisecond)
	if actor.State() != "success" {
		t.Errorf("Expected state success after invocation, got %s", actor.State())
	}
}

func TestActorInvokeError(t *testing.T) {
	m := New[StateID, EventID, Context]("invoke_error").
		Initial("loading").
		State("loading", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Invoke(func(ctx context.Context, c Context) error {
				time.Sleep(20 * time.Millisecond)
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

	actor := Start(m, Context{})
	
	time.Sleep(50 * time.Millisecond)
	if actor.State() != "failure" {
		t.Errorf("Expected state failure after error, got %s", actor.State())
	}
}
