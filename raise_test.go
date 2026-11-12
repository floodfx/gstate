package gstate

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

type raiseKey struct{}

// raiseRecorder collects raised events and signals once n have arrived.
type raiseRecorder struct {
	mu     sync.Mutex
	events []RaisedEvent[StateID, EventID]
	ctxs   []context.Context
	want   int
	done   chan struct{}
}

func newRaiseRecorder(n int) *raiseRecorder {
	return &raiseRecorder{want: n, done: make(chan struct{})}
}

func (r *raiseRecorder) handle(ctx context.Context, e RaisedEvent[StateID, EventID]) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
	r.ctxs = append(r.ctxs, ctx)
	if len(r.events) == r.want {
		close(r.done)
	}
}

func (r *raiseRecorder) wait(t *testing.T) []RaisedEvent[StateID, EventID] {
	t.Helper()
	select {
	case <-r.done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for raised events")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]RaisedEvent[StateID, EventID](nil), r.events...)
}

func TestAfterRaiseDeliversEventToHandler(t *testing.T) {
	// A real timer duration is the behavior under test; the raise handler
	// deterministically signals delivery.
	rec := newRaiseRecorder(1)
	m := New[StateID, EventID, Context]("after-raise").
		Initial("waiting").
		State("waiting", func(s *StateBuilder[StateID, EventID, Context]) {
			s.After(time.Millisecond).Raise("timeout")
		}).
		Build()

	a := Start(m, Context{}, m.WithRaiseHandler(rec.handle), m.WithActorID("a1"))
	defer a.Stop()

	got := rec.wait(t)
	if len(got) != 1 {
		t.Fatalf("got %d raised events, want 1", len(got))
	}
	e := got[0]
	if e.Event != "timeout" || e.Source != "waiting" || e.MachineID != "after-raise" || e.ActorID != "a1" {
		t.Errorf("raised event = %+v", e)
	}
	if e.Timestamp.IsZero() {
		t.Error("raised event has zero timestamp")
	}
	// Targetless raise keeps the configuration.
	if a.State() != "waiting" {
		t.Errorf("state = %q, want waiting", a.State())
	}
}

func TestRaiseWithTargetAndOrder(t *testing.T) {
	rec := newRaiseRecorder(2)
	m := New[StateID, EventID, Context]("raise-target").
		Initial("idle").
		State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("go").Raise("first").Raise("second").GoTo("next")
		}).
		State("next", func(*StateBuilder[StateID, EventID, Context]) {}).
		Build()

	a := Start(m, Context{}, m.WithRaiseHandler(rec.handle))
	defer a.Stop()
	ctx := context.WithValue(context.Background(), raiseKey{}, "trace")
	if err := a.SendCtx(ctx, "go"); err != nil {
		t.Fatal(err)
	}

	got := rec.wait(t)
	if got[0].Event != "first" || got[1].Event != "second" {
		t.Errorf("raised order = %v, %v", got[0].Event, got[1].Event)
	}
	if got[0].Source != "idle" || got[0].Target != "next" {
		t.Errorf("raised event = %+v, want Source=idle Target=next", got[0])
	}
	rec.mu.Lock()
	v := rec.ctxs[0].Value(raiseKey{})
	rec.mu.Unlock()
	if v != "trace" {
		t.Errorf("handler ctx value = %v, want trace", v)
	}
	if a.State() != "next" {
		t.Errorf("state = %q, want next", a.State())
	}
}

func TestRaiseSkippedWhenGuardFails(t *testing.T) {
	rec := newRaiseRecorder(1)
	m := New[StateID, EventID, Context]("raise-guard").
		Initial("idle").
		State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("go").Guard(func(Context) bool { return false }).Raise("blocked")
			s.On("go").Raise("allowed")
		}).
		Build()

	a := Start(m, Context{}, m.WithRaiseHandler(rec.handle))
	defer a.Stop()
	a.Send("go")

	got := rec.wait(t)
	if got[0].Event != "allowed" {
		t.Errorf("raised %q, want allowed", got[0].Event)
	}
}

func TestRaiseHandlerRunsOutsideActorLock(t *testing.T) {
	// The handler reads actor state; this would deadlock if called under the lock.
	states := make(chan StateID, 1)
	var a *Actor[StateID, EventID, Context]
	ready := make(chan struct{})
	m := New[StateID, EventID, Context]("raise-unlocked").
		Initial("idle").
		State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("go").Raise("ping").GoTo("next")
		}).
		State("next", func(*StateBuilder[StateID, EventID, Context]) {}).
		Build()

	a = Start(m, Context{}, m.WithRaiseHandler(func(context.Context, RaisedEvent[StateID, EventID]) {
		<-ready
		states <- a.State()
	}))
	defer a.Stop()
	close(ready)
	a.Send("go")

	select {
	case s := <-states:
		if s != "next" {
			t.Errorf("state seen by handler = %q, want next", s)
		}
	case <-time.After(time.Second):
		t.Fatal("handler deadlocked or never ran")
	}
}

func TestRaiseOnAlwaysAndStartup(t *testing.T) {
	rec := newRaiseRecorder(1)
	m := New[StateID, EventID, Context]("raise-always").
		Initial("boot").
		State("boot", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Always().Raise("booted").GoTo("ready")
		}).
		State("ready", func(*StateBuilder[StateID, EventID, Context]) {}).
		Build()

	a := Start(m, Context{}, m.WithRaiseHandler(rec.handle))
	defer a.Stop()

	got := rec.wait(t)
	if got[0].Event != "booted" || got[0].Source != "boot" || got[0].Target != "ready" {
		t.Errorf("raised event = %+v", got[0])
	}
}

func TestRaiseWithoutHandlerIsNoop(t *testing.T) {
	done := make(chan struct{})
	m := New[StateID, EventID, Context]("raise-nohandler").
		Initial("idle").
		State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("go").Raise("ignored").GoTo("next")
		}).
		State("next", func(*StateBuilder[StateID, EventID, Context]) {}).
		Build()

	obs := ObserverFuncs[StateID, EventID, Context]{
		TransitionFunc: func(context.Context, *TransitionEvent[StateID, EventID, Context]) { close(done) },
	}
	a := Start(m, Context{}, m.WithObservers(obs))
	defer a.Stop()
	a.Send("go")
	<-done
	if a.State() != "next" {
		t.Errorf("state = %q, want next", a.State())
	}
}

func TestRaiseExports(t *testing.T) {
	m := New[StateID, EventID, Context]("raise-export").
		Initial("waiting").
		State("waiting", func(s *StateBuilder[StateID, EventID, Context]) {
			s.After(5 * time.Second).Raise("timeout").GoTo("expired")
		}).
		State("expired", func(*StateBuilder[StateID, EventID, Context]) {}).
		Build()

	if got := ToMermaid(m); !strings.Contains(got, "after 5000ms / raise timeout") {
		t.Errorf("mermaid missing raise label:\n%s", got)
	}

	out, err := ToSCXMLString(m)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `<send event="timeout" target="#_parent"></send>`) {
		t.Errorf("scxml missing raise send:\n%s", out)
	}
}

func TestRaiseHandlerMayCallSend(t *testing.T) {
	done := make(chan struct{})
	var a *Actor[StateID, EventID, Context]
	ready := make(chan struct{})
	m := New[StateID, EventID, Context]("raise-send").
		Initial("idle").
		State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("go").Raise("request").GoTo("waiting")
		}).
		State("waiting", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("ack").GoTo("acked")
		}).
		State("acked", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Entry(func(c Context) Context { close(done); return c })
		}).
		Build()

	a = Start(m, Context{}, m.WithRaiseHandler(func(context.Context, RaisedEvent[StateID, EventID]) {
		<-ready
		a.Send("ack")
	}))
	defer a.Stop()
	close(ready)
	a.Send("go")

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ack sent from raise handler never processed")
	}
}

func TestRaiseDeliveredBeforeAutoStopCompletes(t *testing.T) {
	var mu sync.Mutex
	var got []EventID
	m := New[StateID, EventID, Context]("raise-final").
		Initial("idle").
		State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("finish").Raise("finished").GoTo("end")
		}).
		State("end", func(s *StateBuilder[StateID, EventID, Context]) { s.Type(Final) }).
		Build()

	a := Start(m, Context{}, m.WithRaiseHandler(func(_ context.Context, e RaisedEvent[StateID, EventID]) {
		mu.Lock()
		got = append(got, e.Event)
		mu.Unlock()
	}))
	a.Send("finish")
	select {
	case <-a.Done():
	case <-time.After(time.Second):
		t.Fatal("actor did not auto-stop")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != "finished" {
		t.Errorf("raised before shutdown = %v, want [finished]", got)
	}
}
