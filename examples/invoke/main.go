package main

import (
	"context"
	"fmt"
	"time"

	"github.com/floodfx/gstate"
)

type S string
type E string

func main() {
	// 1. Invoked Services are goroutines that represent external side effects.
	// Common uses: fetching data, starting a timer, or running a background task.
	// CRITICAL: They are automatically cancelled if the state is left.
	machine := gstate.New[S, E, any]("service_manager").
		Initial("loading").
		State("loading", func(s *gstate.StateBuilder[S, E, any]) {
			// s.Invoke starts a new goroutine when we enter 'loading'.
			s.Invoke(func(ctx context.Context, c any) error {
				fmt.Println("  [Invoke] Starting async work...")
				select {
				case <-time.After(100 * time.Millisecond):
					fmt.Println("  [Invoke] Task successfully completed!")
					return nil // Transition to 'success' (onDone)
				case <-ctx.Done():
					fmt.Println("  [Invoke] Task was cancelled by state exit!")
					return ctx.Err() // No transition occurs
				}
			}, "success", "error")
			
			// Allow manual cancellation
			s.On("CANCEL").GoTo("idle")
		}).
		State("success", func(s *gstate.StateBuilder[S, E, any]) {
			s.Type(gstate.Final)
		}).
		State("error", func(s *gstate.StateBuilder[S, E, any]) {
			s.Type(gstate.Final)
		}).
		State("idle", func(s *gstate.StateBuilder[S, E, any]) {
			// A dead-end state to test cancellation
		}).
		Build()

	fmt.Println("--- Test Case 1: Completion ---")
	// Let the service finish naturally.
	actor1 := gstate.Start(machine, nil)
	time.Sleep(150 * time.Millisecond)
	fmt.Printf("Final State: %s\n", actor1.State())

	fmt.Println("\n--- Test Case 2: Cancellation ---")
	// Interrupt the service by sending an event to change states.
	actor2 := gstate.Start(machine, nil)
	time.Sleep(20 * time.Millisecond)
	
	fmt.Println("Action: Sending CANCEL event...")
	actor2.Send("CANCEL")
	
	// Wait a bit to see the cancellation log.
	time.Sleep(50 * time.Millisecond)
	fmt.Printf("Final State: %s\n", actor2.State())

	fmt.Println("\n--- Conclusion ---")
	fmt.Println("Invoked services allow for clean async logic that is")
	fmt.Println("tightly bound to the state lifecycle, preventing resource leaks.")
}
