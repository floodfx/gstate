package main

import (
	"fmt"
	"time"

	"github.com/floodfx/gstate"
)

// 1. Define types for State IDs, Event IDs, and Context.
// Using custom types (~string) provides better safety than raw strings.
type MyState string
type MyEvent string

// MyContext is the data held by the state machine.
// All updates to context are pure functions (Assign).
type MyContext struct {
	Count int
}

func main() {
	// 2. Build the Machine (the "Blueprint")
	// The machine is immutable once built.
	machine := gstate.New[MyState, MyEvent, MyContext]("counter").
		Initial("idle"). // Set the starting state
		
		// Define the 'idle' state
		State("idle", func(s *gstate.StateBuilder[MyState, MyEvent, MyContext]) {
			// Define an internal transition (no state change)
			// Assign is used to modify the context data.
			s.On("INCREMENT").
				Assign(func(c MyContext) MyContext {
					c.Count++
					fmt.Printf("[idle] Incremented count to: %d\n", c.Count)
					return c
				})
			
			// Define a transition to another state
			s.On("START").GoTo("active")
		}).

		// Define the 'active' state
		State("active", func(s *gstate.StateBuilder[MyState, MyEvent, MyContext]) {
			// Entry actions fire when the state is entered.
			s.Entry(func(c MyContext) MyContext {
				fmt.Println("[active] Entering state...")
				return c
			})

			// Exit actions fire when the state is left.
			s.Exit(func(c MyContext) MyContext {
				fmt.Println("[active] Leaving state...")
				return c
			})

			// Transitions back to idle
			s.On("STOP").GoTo("idle")
		}).
		Build()

	// 3. Start the Actor (the "Execution")
	// The Actor holds the specific instance's state and context.
	fmt.Println("--- Starting Actor ---")
	actor := gstate.Start(machine, MyContext{Count: 0})

	// 4. Send Events
	// Send is non-blocking and queues the event in the actor's mailbox.
	actor.Send("INCREMENT")
	actor.Send("INCREMENT")
	
	// Wait a moment for the sequential mailbox to process
	time.Sleep(10 * time.Millisecond)
	fmt.Printf("Current state: %s, Count: %d\n", actor.State(), actor.Snapshot().Context.Count)

	fmt.Println("\n--- Moving to Active ---")
	actor.Send("START")
	time.Sleep(10 * time.Millisecond)
	fmt.Printf("Current state: %s\n", actor.State())

	fmt.Println("\n--- Stopping ---")
	actor.Send("STOP")
	time.Sleep(10 * time.Millisecond)
	fmt.Printf("Final state: %s, Count: %d\n", actor.State(), actor.Snapshot().Context.Count)
}
