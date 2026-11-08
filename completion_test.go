package gstate

import (
	"context"
	"errors"
	"testing"
)

func TestNestedFinalDoesNotTerminateSession(t *testing.T) {
	m := New[StateID, EventID, Context]("local-final").Initial("root").
		State("root", func(root *StateBuilder[StateID, EventID, Context]) {
			root.Initial("workflow")
			root.On("PING").Assign(func(d Context) Context { d.Count++; return d })
			root.State("workflow", func(flow *StateBuilder[StateID, EventID, Context]) {
				flow.Initial("finished")
				flow.State("finished", func(done *StateBuilder[StateID, EventID, Context]) { done.Type(Final) })
			})
		}).Build()
	a := Start(m, Context{})
	defer a.Stop()
	if err := a.SendCtx(context.Background(), "PING"); errors.Is(err, ErrActorStopped) {
		t.Fatal("a nested final child terminated the enclosing session")
	}
}
