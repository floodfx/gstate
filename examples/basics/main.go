package main

import (
	"fmt"
	"time"

	"github.com/floodfx/gstate"
)

// 1. Define types for State IDs, Event IDs, and Context.
type MyState string
type MyEvent string

// 2. Define constants for our states and events.
// This ensures type safety and prevents typos throughout the application.
const (
	StateIdle   MyState = "idle"
	StateActive MyState = "active"
)

const (
	EventIncrement MyEvent = "INCREMENT"
	EventStart     MyEvent = "START"
	EventStop      MyEvent = "STOP"
)

// MyContext is the data held by the state machine.
type MyContext struct {
	Count int
}

func main() {
	// 3. Build the Machine using the defined constants.
	machine := gstate.New[MyState, MyEvent, MyContext]("counter").
		Initial(StateIdle).
		
		State(StateIdle, func(s *gstate.StateBuilder[MyState, MyEvent, MyContext]) {
			s.On(EventIncrement).
				Assign(func(c MyContext) MyContext {
					c.Count++
					fmt.Printf("[idle] Incremented count to: %d\n", c.Count)
					return c
				})
			
			s.On(EventStart).GoTo(StateActive)
		}).

		State(StateActive, func(s *gstate.StateBuilder[MyState, MyEvent, MyContext]) {
			s.Entry(func(c MyContext) MyContext {
				fmt.Println("[active] Entering state...")
				return c
			})

			s.Exit(func(c MyContext) MyContext {
				fmt.Println("[active] Leaving state...")
				return c
			})

			s.On(EventStop).GoTo(StateIdle)
		}).
		Build()

	fmt.Println("--- Starting Actor ---")
	actor := gstate.Start(machine, MyContext{Count: 0})

	// 4. Send Events using constants
	actor.Send(EventIncrement)
	actor.Send(EventIncrement)
	
	time.Sleep(10 * time.Millisecond)
	fmt.Printf("Current state: %s, Count: %d\n", actor.State(), actor.Snapshot().Context.Count)

	fmt.Println("\n--- Moving to Active ---")
	actor.Send(EventStart)
	time.Sleep(10 * time.Millisecond)
	fmt.Printf("Current state: %s\n", actor.State())

	fmt.Println("\n--- Stopping ---")
	actor.Send(EventStop)
	time.Sleep(10 * time.Millisecond)
	fmt.Printf("Final state: %s, Count: %d\n", actor.State(), actor.Snapshot().Context.Count)
}
