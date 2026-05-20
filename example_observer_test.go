package gstate_test

import (
	"context"
	"fmt"

	"github.com/floodfx/gstate"
)

type myState string
type myEvent string
type myCtx struct{ Count int }

func (c myCtx) Clone() myCtx {
	return c
}

func buildExampleMachine() *gstate.Machine[myState, myEvent, myCtx] {
	return gstate.New[myState, myEvent, myCtx]("example").
		Initial("idle").
		State("idle", func(s *gstate.StateBuilder[myState, myEvent, myCtx]) {
			s.On("GO").
				Guard(func(_ myCtx) bool { return true }).
				Assign(func(c myCtx) myCtx { c.Count++; return c }).
				GoTo("active")
		}).
		State("active", func(_ *gstate.StateBuilder[myState, myEvent, myCtx]) {}).
		Build()
}

// transitionBarrier is a channel-backed observer used by examples to wait
// deterministically for OnTransition without time.Sleep / polling.
type transitionBarrier struct {
	gstate.NopObserver[myState, myEvent, myCtx]
	done chan struct{}
}

func (b *transitionBarrier) OnTransition(_ context.Context, _ gstate.TransitionEvent[myState, myEvent, myCtx]) {
	select {
	case b.done <- struct{}{}:
	default:
	}
}

// ExampleRecordingObserver attaches a RecordingObserver to a tiny machine,
// sends one event, and prints the kinds of lifecycle events recorded. A
// transitionBarrier composed via MultiObserver synchronises the example so
// it doesn't need a sleep.
func ExampleRecordingObserver() {
	rec := &gstate.RecordingObserver[myState, myEvent, myCtx]{}
	bar := &transitionBarrier{done: make(chan struct{}, 1)}
	m := buildExampleMachine()
	a := gstate.Start(m, myCtx{},
		m.WithObserver(gstate.MultiObserver[myState, myEvent, myCtx]{rec, bar}),
	)
	defer a.Stop()

	a.Send("GO")
	<-bar.done // deterministic: blocks until OnTransition fires

	for _, ev := range rec.Events() {
		fmt.Println(ev.Kind)
	}
	// Output:
	// state_entered
	// event_received
	// guard
	// state_exited
	// action
	// state_entered
	// transition
}

// loggingObserver embeds NopObserver and overrides only OnTransition. It
// publishes formatted lines through a buffered channel so the example can
// observe them safely without a sleep+racy read.
type loggingObserver struct {
	gstate.NopObserver[myState, myEvent, myCtx]
	lines chan string
}

func (l *loggingObserver) OnTransition(_ context.Context, e gstate.TransitionEvent[myState, myEvent, myCtx]) {
	l.lines <- fmt.Sprintf("%s --%s--> %s", e.From, e.Event, e.To)
}

// ExampleObserver demonstrates the NopObserver embedding pattern for
// implementing only a subset of Observer methods.
func ExampleObserver() {
	obs := &loggingObserver{lines: make(chan string, 4)}
	m := buildExampleMachine()
	a := gstate.Start(m, myCtx{}, m.WithObserver(obs))
	defer a.Stop()

	a.Send("GO")
	fmt.Println(<-obs.lines)
	// Output:
	// idle --GO--> active
}
