package gstate_test

import (
	"context"
	"fmt"
	"time"

	"github.com/floodfx/gstate"
)

type myState string
type myEvent string
type myCtx struct{ Count int }

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

// ExampleRecordingObserver attaches a RecordingObserver to a tiny machine,
// sends one event, and prints the kinds of lifecycle events recorded.
func ExampleRecordingObserver() {
	rec := &gstate.RecordingObserver[myState, myEvent, myCtx]{}
	a := gstate.Start(buildExampleMachine(), myCtx{},
		gstate.WithObserver[myState, myEvent, myCtx](rec),
	)
	defer a.Stop()

	a.Send("GO")
	// Wait deterministically for the OnTransition that closes the sequence.
	waitFor(rec, gstate.KindTransition, time.Second)

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

// waitFor blocks until the recorder has seen at least one event of the given
// kind, or the timeout elapses. It polls because the recorder's API is
// snapshot-based; for production code a channel-backed observer (see
// ExampleObserver) is the better synchronisation pattern.
func waitFor(rec *gstate.RecordingObserver[myState, myEvent, myCtx], kind string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(rec.Events(kind)) > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
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
	a := gstate.Start(buildExampleMachine(), myCtx{},
		gstate.WithObserver[myState, myEvent, myCtx](obs),
	)
	defer a.Stop()

	a.Send("GO")

	select {
	case line := <-obs.lines:
		fmt.Println(line)
	case <-time.After(time.Second):
		fmt.Println("(timed out)")
	}
	// Output:
	// idle --GO--> active
}
