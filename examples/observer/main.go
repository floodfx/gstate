// Package main demonstrates attaching an Observer to a gstate machine to
// inspect lifecycle events end-to-end.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/floodfx/gstate"
)

type State string
type Event string

const (
	Idle    State = "idle"
	Loading State = "loading"
	Done    State = "done"

	EventStart Event = "START"
)

type Context struct {
	Attempts int
}

// loggingObserver embeds NopObserver and overrides just the methods we want
// to surface in stdout. It is a typical shape: small, focused, free to ignore
// most callbacks.
type loggingObserver struct {
	gstate.NopObserver[State, Event, Context]
}

func (o *loggingObserver) OnTransition(_ context.Context, e gstate.TransitionEvent[State, Event, Context]) {
	fmt.Printf("[%s] %s --%s--> %s\n", e.ActorID, e.From, e.Event, e.To)
}

func (o *loggingObserver) OnInvokeStarted(_ context.Context, e gstate.InvokeEvent[State, Event, Context]) {
	fmt.Printf("[%s] invoke started in %s\n", e.ActorID, e.State)
}

func (o *loggingObserver) OnInvokeCompleted(_ context.Context, e gstate.InvokeEvent[State, Event, Context]) {
	if e.Error != nil {
		fmt.Printf("[%s] invoke in %s completed with error after %v: %v\n", e.ActorID, e.State, e.Duration, e.Error)
		return
	}
	fmt.Printf("[%s] invoke in %s completed in %v\n", e.ActorID, e.State, e.Duration)
}

func main() {
	machine := gstate.New[State, Event, Context]("observer-demo").
		Initial(Idle).
		State(Idle, func(s *gstate.StateBuilder[State, Event, Context]) {
			s.On(EventStart).GoTo(Loading)
		}).
		State(Loading, func(s *gstate.StateBuilder[State, Event, Context]) {
			s.Invoke(func(_ context.Context, _ Context) error {
				time.Sleep(50 * time.Millisecond)
				return nil
			}, Done, Done)
		}).
		State(Done, func(s *gstate.StateBuilder[State, Event, Context]) {
			s.Type(gstate.Final)
		}).
		Build()

	// Combine a logger for live tracing with a recorder for post-hoc inspection.
	logger := &loggingObserver{}
	rec := &gstate.RecordingObserver[State, Event, Context]{}

	actor := gstate.Start(machine, Context{},
		machine.WithObserver(gstate.MultiObserver[State, Event, Context]{logger, rec}),
	)
	defer actor.Stop()

	fmt.Println("ActorID:", actor.ID())
	actor.Send(EventStart)

	// Wait until the invoke completes and we land in Done.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && actor.State() != Done {
		time.Sleep(5 * time.Millisecond)
	}

	fmt.Println("\nRecorded events:")
	for _, ev := range rec.Events() {
		// RecordedEvent.String() delegates to the payload's String() method.
		fmt.Printf("  %s\n", ev)
	}

	// Payloads carry json tags so they can be serialized for off-process
	// transport (e.g. shipping to a telemetry pipeline).
	fmt.Println("\nLast transition as JSON:")
	transitions := rec.Transitions()
	if len(transitions) > 0 {
		b, _ := json.MarshalIndent(transitions[len(transitions)-1], "  ", "  ")
		fmt.Printf("  %s\n", b)
	}
}
