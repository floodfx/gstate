package main

import (
	"fmt"
	"time"

	"github.com/floodfx/gstate"
)

type S string
type E string

func main() {
	// 1. Parallel States allow a machine to be in multiple states simultaneously.
	// This is useful for systems with orthogonal (independent) logic.
	// In this example, a computer's Input system tracks Keyboard and Mouse states independently.
	machine := gstate.New[S, E, any]("input_system").
		Initial("active").
		State("active", func(s *gstate.StateBuilder[S, E, any]) {
			// Set the type to Parallel. This means ALL immediate children will be active.
			s.Type(gstate.Parallel)

			// 2. Define the 'keyboard' region.
			s.State("keyboard", func(s *gstate.StateBuilder[S, E, any]) {
				s.Initial("caps_off")
				s.State("caps_off", func(s *gstate.StateBuilder[S, E, any]) {
					s.On("CAPS_LOCK").GoTo("caps_on")
				})
				s.State("caps_on", func(s *gstate.StateBuilder[S, E, any]) {
					s.On("CAPS_LOCK").GoTo("caps_off")
				})
			})

			// 3. Define the 'mouse' region.
			// Transitions here do NOT affect the keyboard region.
			s.State("mouse", func(s *gstate.StateBuilder[S, E, any]) {
				s.Initial("not_clicked")
				s.State("not_clicked", func(s *gstate.StateBuilder[S, E, any]) {
					s.On("CLICK").GoTo("clicked")
				})
				s.State("clicked", func(s *gstate.StateBuilder[S, E, any]) {
					s.On("RELEASE").GoTo("not_clicked")
				})
			})
		}).
		Build()

	fmt.Println("--- Starting Parallel Actor ---")
	actor := gstate.Start(machine, nil)
	
	// Notice that we are in multiple leaf states at once.
	fmt.Printf("Active States: %v\n", actor.States())

	fmt.Println("\n--- Sending 'CLICK' ---")
	// This only affects the 'mouse' region.
	actor.Send("CLICK")
	time.Sleep(10 * time.Millisecond)
	fmt.Printf("Active States: %v\n", actor.States())

	fmt.Println("\n--- Sending 'CAPS_LOCK' ---")
	// This only affects the 'keyboard' region.
	actor.Send("CAPS_LOCK")
	time.Sleep(10 * time.Millisecond)
	fmt.Printf("Active States: %v\n", actor.States())

	fmt.Println("\n--- Conclusion ---")
	fmt.Println("Parallel states avoid the 'state explosion' problem by letting you")
	fmt.Println("model independent behaviors without creating states for every combination.")
}
