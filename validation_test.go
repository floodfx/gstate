package gstate

import (
	"context"
	"strings"
	"testing"
	"time"
)

func assertPanic(t *testing.T, fn func(), expectedSubstr string) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Errorf("expected panic containing %q, but code did not panic", expectedSubstr)
			return
		}
		var errStr string
		switch v := r.(type) {
		case error:
			errStr = v.Error()
		case string:
			errStr = v
		default:
			t.Errorf("unexpected panic value type: %T", r)
			return
		}
		if !strings.Contains(errStr, expectedSubstr) {
			t.Errorf("expected panic containing %q, got: %q", expectedSubstr, errStr)
		}
	}()
	fn()
}

func TestValidation_ValidMachine(t *testing.T) {
	// A fully correct machine definition should compile without panicking.
	_ = New[StateID, EventID, Context]("valid").
		Initial("idle").
		State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("START").GoTo("active")
		}).
		State("active", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Initial("nested_idle")
			s.State("nested_idle", func(s2 *StateBuilder[StateID, EventID, Context]) {
				s2.Always().GoTo("nested_done")
			})
			s.State("nested_done", func(s2 *StateBuilder[StateID, EventID, Context]) {
				s2.After(10 * time.Millisecond).GoTo("nested_idle")
			})
			s.Invoke(func(ctx context.Context, c Context) error { return nil }, "completed", "failed")
		}).
		State("completed", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Type(Final)
		}).
		State("failed", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Type(Final)
		}).
		Build()
}

func TestValidation_MissingTopLevelInitial(t *testing.T) {
	assertPanic(t, func() {
		_ = New[StateID, EventID, Context]("test").
			Initial("non_existent").
			State("idle", func(s *StateBuilder[StateID, EventID, Context]) {}).
			Build()
	}, "gstate: machine \"test\" has invalid initial state: \"non_existent\" does not exist")
}

func TestValidation_MissingCompoundInitial(t *testing.T) {
	assertPanic(t, func() {
		_ = New[StateID, EventID, Context]("test").
			Initial("parent").
			State("parent", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Initial("non_existent_child")
				s.State("child1", func(s2 *StateBuilder[StateID, EventID, Context]) {})
			}).
			Build()
	}, "gstate: machine \"test\" compound state \"parent\" has invalid initial state: \"non_existent_child\" is not a direct child state")
}

func TestValidation_InvalidTransitionTarget(t *testing.T) {
	assertPanic(t, func() {
		_ = New[StateID, EventID, Context]("test").
			Initial("idle").
			State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
				s.On("START").GoTo("non_existent")
			}).
			Build()
	}, "gstate: machine \"test\" state \"idle\" has invalid transition on event \"START\": target \"non_existent\" does not exist")
}

func TestValidation_InvalidAlwaysTarget(t *testing.T) {
	assertPanic(t, func() {
		_ = New[StateID, EventID, Context]("test").
			Initial("idle").
			State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Always().GoTo("non_existent")
			}).
			Build()
	}, "gstate: machine \"test\" state \"idle\" has invalid Always transition: target \"non_existent\" does not exist")
}

func TestValidation_InvalidAfterTarget(t *testing.T) {
	assertPanic(t, func() {
		_ = New[StateID, EventID, Context]("test").
			Initial("idle").
			State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
				s.After(10 * time.Millisecond).GoTo("non_existent")
			}).
			Build()
	}, "gstate: machine \"test\" state \"idle\" has invalid Delayed transition: target \"non_existent\" does not exist")
}

func TestValidation_InvalidInvokeDoneTarget(t *testing.T) {
	assertPanic(t, func() {
		_ = New[StateID, EventID, Context]("test").
			Initial("idle").
			State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Invoke(func(ctx context.Context, c Context) error { return nil }, "non_existent", "idle")
			}).
			Build()
	}, "gstate: machine \"test\" state \"idle\" has invalid Invoke OnDone target: \"non_existent\" does not exist")
}

func TestValidation_InvalidInvokeErrorTarget(t *testing.T) {
	assertPanic(t, func() {
		_ = New[StateID, EventID, Context]("test").
			Initial("idle").
			State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Invoke(func(ctx context.Context, c Context) error { return nil }, "idle", "non_existent")
			}).
			Build()
	}, "gstate: machine \"test\" state \"idle\" has invalid Invoke OnError target: \"non_existent\" does not exist")
}
