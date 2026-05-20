package main

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/floodfx/gstate"
)

type MyState string
type MyEvent string

// MyData is our context data. It MUST be JSON serializable for persistence.
type MyData struct {
	Value string `json:"value"`
}

func (c MyData) Clone() MyData {
	return c
}

func main() {
	// 1. Persistence and Hydration allow you to save and restore the state of an actor.
	// This is critical for long-running workflows that must survive app restarts.
	machine := gstate.New[MyState, MyEvent, MyData]("persistence_demo").
		Initial("one").
		State("one", func(s *gstate.StateBuilder[MyState, MyEvent, MyData]) {
			s.On("NEXT").
				Assign(func(c MyData) MyData { c.Value = "step1_complete"; return c }).
				GoTo("two")
		}).
		State("two", func(s *gstate.StateBuilder[MyState, MyEvent, MyData]) {
			s.On("FINISH").
				Assign(func(c MyData) MyData { c.Value = "step2_complete"; return c }).
				GoTo("three")
		}).
		State("three", func(s *gstate.StateBuilder[MyState, MyEvent, MyData]) {
			s.Type(gstate.Final)
		}).
		Build()

	fmt.Println("--- Step 1: Start Actor and Trigger a Transition ---")
	actor1 := gstate.Start(machine, MyData{Value: "initial"})
	actor1.Send("NEXT")
	time.Sleep(10 * time.Millisecond)

	// 2. Snapshot the current state.
	// This returns a Snapshot struct containing:
	// - Active States
	// - History map
	// - MyData data
	snapshot := actor1.Snapshot()

	// Convert snapshot to JSON. This could be saved to a database.
	data, _ := json.MarshalIndent(snapshot, "", "  ")
	fmt.Printf("Serialized Snapshot:\n%s\n", string(data))

	fmt.Println("\n--- Step 2: Hydrate a New Actor from the Snapshot ---")
	// Simulate loading the JSON back.
	var loadedSnapshot gstate.Snapshot[MyState, MyData]
	if err := json.Unmarshal(data, &loadedSnapshot); err != nil {
		log.Fatalf("unmarshal snapshot: %v", err)
	}

	// gstate.Hydrate creates a new Actor in exactly the same state.
	actor2 := gstate.Hydrate(machine, loadedSnapshot)
	fmt.Printf("Hydrated State: %s\n", actor2.State())
	fmt.Printf("Hydrated Data: %s\n", actor2.Snapshot().Data.Value)

	fmt.Println("\n--- Step 3: Continue Workflow from Hydrated Actor ---")
	actor2.Send("FINISH")
	time.Sleep(10 * time.Millisecond)
	fmt.Printf("Final State: %s\n", actor2.State())
	fmt.Printf("Final Data: %s\n", actor2.Snapshot().Data.Value)

	fmt.Println("\n--- Conclusion ---")
	fmt.Println("Persistence allows you to decouple the machine execution from")
	fmt.Println("the process lifecycle, enabling durable state charts.")
}
