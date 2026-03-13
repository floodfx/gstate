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
	// 1. Hierarchical States allow nesting state machines inside other states.
	// This helps group logic and share behavior across states.
	machine := gstate.New[MyState, MyEvent, MyContext]("hierarchy").
		Initial("parent").
		State("parent", func(s *gstate.StateBuilder[MyState, MyEvent, MyContext]) {
			// Set the default child to enter when 'parent' is entered.
			s.Initial("child1")
			
			// Actions defined on a parent run for ALL entries into children.
			s.Entry(func(c MyContext) MyContext { fmt.Println("[parent] Entering..."); return c })
			s.Exit(func(c MyContext) MyContext { fmt.Println("[parent] Exiting..."); return c })
			
			// 2. Event Bubbling:
			// If a child doesn't handle an event, it "bubbles up" to the parent.
			// Here, any child receiving 'EXIT_ALL' will cause the parent to transition.
			s.On("EXIT_ALL").GoTo("done")
			
			// 3. Nested States (Sub-states)
			s.State("child1", func(s *gstate.StateBuilder[MyState, MyEvent, MyContext]) {
				s.Entry(func(c MyContext) MyContext { fmt.Println("  [child1] Entering..."); return c })
				s.Exit(func(c MyContext) MyContext { fmt.Println("  [child1] Exiting..."); return c })
				s.On("TO_CHILD2").GoTo("child2")
			})

			s.State("child2", func(s *gstate.StateBuilder[MyState, MyEvent, MyContext]) {
				s.Entry(func(c MyContext) MyContext { fmt.Println("  [child2] Entering..."); return c })
				s.Exit(func(c MyContext) MyContext { fmt.Println("  [child2] Exiting..."); return c })
				s.On("TO_CHILD1").GoTo("child1")
			})
		}).
		State("done", func(s *gstate.StateBuilder[MyState, MyEvent, MyContext]) {
			s.Type(gstate.Final)
		}).
		Build()

	fmt.Println("--- Starting Actor ---")
	actor := gstate.Start(machine, nil)
	
	// actor.States() returns ALL active states from root to leaf.
	fmt.Printf("Initial States Stack: %v\n", actor.States())

	fmt.Println("\n--- Sending 'TO_CHILD2' ---")
	actor.Send("TO_CHILD2")
	time.Sleep(10 * time.Millisecond)
	fmt.Printf("States Stack: %v\n", actor.States())

	fmt.Println("\n--- Sending 'EXIT_ALL' (Bubbles to Parent) ---")
	// child2 doesn't define 'EXIT_ALL', so 'parent' handles it.
	actor.Send("EXIT_ALL")
	time.Sleep(10 * time.Millisecond)
	fmt.Printf("States Stack: %v\n", actor.States())
}
