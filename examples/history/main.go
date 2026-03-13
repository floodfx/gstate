package main

import (
	"fmt"
	"time"

	"github.com/floodfx/gstate"
)

type S string
type E string

func main() {
	// 1. History States allow a compound state to remember which of its children
	// was active before the state was exited.
	// This is useful for things like pausing a task and resuming exactly where you were.
	machine := gstate.New[S, E, any]("history_demo").
		Initial("app").
		State("app", func(s *gstate.StateBuilder[S, E, any]) {
			// Set History to Shallow. It will remember the direct active child.
			s.History(gstate.Shallow)
			s.Initial("screen1")

			s.State("screen1", func(s *gstate.StateBuilder[S, E, any]) {
				s.On("SWITCH").GoTo("screen2")
			})

			s.State("screen2", func(s *gstate.StateBuilder[S, E, any]) {
				s.On("SWITCH").GoTo("screen1")
			})

			// 2. An event that moves us COMPLETELY OUT of 'app'.
			s.On("GO_IDLE").GoTo("idle")
		}).
		State("idle", func(s *gstate.StateBuilder[S, E, any]) {
			// An event that moves us back into 'app'.
			// Because 'app' has History enabled, it will bypass its 'Initial'
			// state if there is a remembered state.
			s.On("WAKE").GoTo("app")
		}).
		Build()

	fmt.Println("--- Starting Actor ---")
	actor := gstate.Start(machine, nil)
	fmt.Printf("Initial: %s\n", actor.State())

	fmt.Println("\n--- Switching Screen to screen2 ---")
	actor.Send("SWITCH")
	time.Sleep(10 * time.Millisecond)
	fmt.Printf("Current: %s\n", actor.State())

	fmt.Println("\n--- Going to IDLE (Leaving 'app' completely) ---")
	actor.Send("GO_IDLE")
	time.Sleep(10 * time.Millisecond)
	fmt.Printf("Current: %s\n", actor.State())

	fmt.Println("\n--- Waking up (WAKE triggers GoTo('app')) ---")
	// Normally, GoTo('app') would always land in 'screen1' (Initial).
	// But since 'app' has history, we land back in 'screen2'.
	actor.Send("WAKE")
	time.Sleep(10 * time.Millisecond)
	fmt.Printf("Resumed: %s\n", actor.State())

	fmt.Println("\n--- Conclusion ---")
	fmt.Println("History states make it easy to restore deep state hierarchy")
	fmt.Println("without manual bookkeeping or flags.")
}
