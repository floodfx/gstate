package gstate

import (
	"context"
	"testing"
	"time"
)

func TestAlwaysTransitionEmitsObserverCalls(t *testing.T) {
	rec := &RecordingObserver[StateID, EventID, Context]{}
	m := New[StateID, EventID, Context]("always_obs").
		Initial("a").
		State("a", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Always().Guard(func(_ Context) bool { return true }).GoTo("b")
		}).
		State("b", func(_ *StateBuilder[StateID, EventID, Context]) {}).
		Build()

	bar := newKindBarrier(KindTransition, 1)
	a := Start(m, Context{}, m.WithObserver(MultiObserver[StateID, EventID, Context]{rec, bar}))
	defer a.Stop()

	<-bar.done

	guards := rec.Guards()
	if len(guards) == 0 {
		t.Fatal("expected at least one guard evaluation")
	}
	var zeroEvent EventID
	if guards[0].Event != zeroEvent {
		t.Errorf("Always guard Event = %q, want zero value", guards[0].Event)
	}

	transitions := rec.Transitions()
	if len(transitions) == 0 {
		t.Fatal("expected at least one transition")
	}
	last := transitions[len(transitions)-1]
	if last.Event != zeroEvent {
		t.Errorf("Always transition Event = %q, want zero value", last.Event)
	}
	if last.From != "a" || last.To != "b" {
		t.Errorf("Always transition From/To = %q/%q, want a/b", last.From, last.To)
	}
}

// ctxCapture records the ctx of every observer call into a per-kind channel.
type ctxCapture struct {
	NopObserver[StateID, EventID, Context]
	transitions chan context.Context
}

func (c *ctxCapture) OnTransition(ctx context.Context, _ TransitionEvent[StateID, EventID, Context]) {
	select {
	case c.transitions <- ctx:
	default:
	}
}

func TestDelayedTransitionFiresWithBackgroundContext(t *testing.T) {
	cap := &ctxCapture{transitions: make(chan context.Context, 4)}
	m := New[StateID, EventID, Context]("delayed_obs").
		Initial("a").
		State("a", func(s *StateBuilder[StateID, EventID, Context]) {
			s.After(5 * time.Millisecond).GoTo("b")
		}).
		State("b", func(_ *StateBuilder[StateID, EventID, Context]) {}).
		Build()

	a := Start(m, Context{}, m.WithObserver(cap))
	defer a.Stop()

	select {
	case ctx := <-cap.transitions:
		// Should be context.Background() for the delayed transition.
		// (Initial state may also emit OnTransition? No — initial state entry
		// only emits OnStateEntered, not OnTransition.) So the first transition
		// here is the delayed one.
		if ctx == nil {
			t.Fatal("delayed transition ctx is nil")
		}
		if _, ok := ctx.Deadline(); ok {
			t.Errorf("delayed ctx unexpectedly has a deadline")
		}
	case <-time.After(time.Second):
		t.Fatal("delayed transition never fired")
	}
}

func TestAlwaysChainedAfterEventReusesSendCtx(t *testing.T) {
	cap := &ctxCapture{transitions: make(chan context.Context, 8)}
	m := New[StateID, EventID, Context]("always_chain").
		Initial("a").
		State("a", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("GO").GoTo("mid")
		}).
		State("mid", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Always().GoTo("b")
		}).
		State("b", func(_ *StateBuilder[StateID, EventID, Context]) {}).
		Build()

	a := Start(m, Context{}, m.WithObserver(cap))
	defer a.Stop()

	type key struct{}
	parent := context.WithValue(context.Background(), key{}, "trace-1")
	_ = a.SendCtx(parent, "GO")

	// We expect two transitions: a->mid (event GO) and mid->b (Always). Both
	// should carry the parent ctx.
	var got []context.Context
	deadline := time.After(time.Second)
loop:
	for len(got) < 2 {
		select {
		case ctx := <-cap.transitions:
			got = append(got, ctx)
		case <-deadline:
			break loop
		}
	}
	if len(got) < 2 {
		t.Fatalf("got %d transitions, want 2", len(got))
	}
	for i, ctx := range got {
		if ctx.Value(key{}) != "trace-1" {
			t.Errorf("transition %d ctx missing trace-1 value (got %v)", i, ctx.Value(key{}))
		}
	}
}
