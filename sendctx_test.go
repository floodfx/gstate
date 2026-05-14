package gstate

import (
	"context"
	"testing"
	"time"
)

// ctxRecorder captures the context.Context passed to OnEventReceived.
type ctxRecorder struct {
	NopObserver[StateID, EventID, Context]
	got chan context.Context
}

func (c *ctxRecorder) OnEventReceived(ctx context.Context, _ EventNotice[StateID, EventID, Context]) {
	select {
	case c.got <- ctx:
	default:
	}
}

func TestSendCtxPropagatesContext(t *testing.T) {
	m := tinyMachine()
	rec := &ctxRecorder{got: make(chan context.Context, 1)}
	a := Start(m, Context{}, WithObserver[StateID, EventID, Context](rec))
	defer a.Stop()

	type key struct{}
	want := context.WithValue(context.Background(), key{}, "hello")
	a.SendCtx(want, "GO")

	select {
	case got := <-rec.got:
		if got.Value(key{}) != "hello" {
			t.Errorf("ctx value not threaded; got %v", got.Value(key{}))
		}
	case <-time.After(time.Second):
		t.Fatal("OnEventReceived never fired")
	}
}

func TestSendUsesBackgroundContext(t *testing.T) {
	m := tinyMachine()
	rec := &ctxRecorder{got: make(chan context.Context, 1)}
	a := Start(m, Context{}, WithObserver[StateID, EventID, Context](rec))
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
