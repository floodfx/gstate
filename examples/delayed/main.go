package main

import (
	"fmt"
	"time"

	"github.com/floodfx/gstate"
)

type MyState string
type MyEvent string
type MyContext any

func main() {
	// 1. Delayed Transitions are transitions that happen automatically
	// after a specified time.time.Duration.
	// This is commonly used for timeouts, heartbeats, or debouncing.
	machine := gstate.New[MyState, MyEvent, MyContext]("timeout_demo").
		Initial("waiting").
		State("waiting", func(s *gstate.StateBuilder[MyState, MyEvent, MyContext]) {
			fmt.Println("[waiting] State entered. Starting 100ms timer...")
			
			// If we stay in this state for 100ms, move to 'timeout'.
			// The timer is automatically stopped if we leave the state earlier.
			s.After(100 * time.Millisecond).GoTo("timeout")
			
			// Define an event that could move us away before the timeout hits.
			s.On("USER_ACTION").GoTo("other_state")
		}).
		State("timeout", func(s *gstate.StateBuilder[MyState, MyEvent, MyContext]) {
			fmt.Println("[timeout] The timer fired! Transitioned successfully.")
		}).
		State("other_state", func(s *gstate.StateBuilder[MyState, MyEvent, MyContext]) {
			fmt.Println("[other_state] Left 'waiting' before timeout.")
		}).
		Build()

	fmt.Println("--- Test Case 1: Reaching the Timeout ---")
	gstate.Start(machine, nil)
	time.Sleep(150 * time.Millisecond) // Let it time out
	
	fmt.Println("\n--- Test Case 2: Escaping before Timeout ---")
	actor2 := gstate.Start(machine, nil)
	time.Sleep(20 * time.Millisecond) // Wait a tiny bit
	
	fmt.Println("Action: Sending 'USER_ACTION' before 100ms is up...")
	actor2.Send("USER_ACTION")
	
	// Wait to see if the timeout still fires (it shouldn't).
	time.Sleep(150 * time.Millisecond)

	fmt.Println("\n--- Conclusion ---")
	fmt.Println("Delayed transitions handle complex time-based logic natively,")
	fmt.Println("eliminating the need for manual time.Sleep() or timers.")
}
