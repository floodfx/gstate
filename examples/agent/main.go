package main

import (
	"context"
	"fmt"
	"time"

	"github.com/floodfx/gstate"
)

type AgentState string
type AgentEvent string

// AgentCtx holds the state of our autonomous agent.
type AgentCtx struct {
	Retries     int
	FixAttempts int
	RepoDir     string
}

func main() {
	// 1. This example combines multiple features: Invoke, Always, Guard, and Assign.
	// It models a developer agent that investigates a problem, fixes it, and verifies.
	machine := gstate.New[AgentState, AgentEvent, AgentCtx]("agent").
		Initial("investigating").
		State("investigating", func(s *gstate.StateBuilder[AgentState, AgentEvent, AgentCtx]) {
			// Start async work when we enter 'investigating'.
			s.Invoke(func(ctx context.Context, c AgentCtx) error {
				fmt.Println("-> [investigating] Running diagnostics...")
				time.Sleep(100 * time.Millisecond)
				return nil
			}, "approving", "done")
		}).
		State("approving", func(s *gstate.StateBuilder[AgentState, AgentEvent, AgentCtx]) {
			// Wait for human/system input
			s.On("YES").GoTo("fixing")
			s.On("NO").GoTo("done")
		}).
		State("fixing", func(s *gstate.StateBuilder[AgentState, AgentEvent, AgentCtx]) {
			// 2. Transient Transition (Always).
			// If FixAttempts >= 2, we immediately move to 'done' without waiting for an event.
			s.Always().
				Guard(func(c AgentCtx) bool { return c.FixAttempts >= 2 }).
				Assign(func(c AgentCtx) AgentCtx {
					fmt.Println("-> [fixing] Max fix attempts reached. Giving up.")
					return c
				}).
				GoTo("done")

			s.On("SUCCESS").GoTo("verifying")
			
			// If we retry, we increment a counter and go back to investigate.
			s.On("RETRY").
				Assign(func(c AgentCtx) AgentCtx {
					c.FixAttempts++
					fmt.Printf("-> [fixing] Retry attempt %d\n", c.FixAttempts)
					return c
				}).
				GoTo("investigating")
		}).
		State("verifying", func(s *gstate.StateBuilder[AgentState, AgentEvent, AgentCtx]) {
			// 3. Conditional Transition (Guard).
			// If verification fails, we can retry 'fixing' if we haven't hit the retry limit.
			s.On("FAIL").
				Guard(func(c AgentCtx) bool { return c.Retries < 2 }).
				Assign(func(c AgentCtx) AgentCtx {
					c.Retries++
					fmt.Printf("-> [verifying] Retry count: %d\n", c.Retries)
					return c
				}).
				GoTo("fixing")

			s.On("FAIL").GoTo("done") // Fallback if Guard fails
			s.On("PASS").GoTo("done")
		}).
		State("done", func(s *gstate.StateBuilder[AgentState, AgentEvent, AgentCtx]) {
			s.Type(gstate.Final)
			s.Entry(func(c AgentCtx) AgentCtx {
				fmt.Println("-> [done] Agent workflow complete.")
				return c
			})
		}).
		Build()

	fmt.Println("--- Starting Agent Actor ---")
	actor := gstate.Start(machine, AgentCtx{RepoDir: "./workspace"})

	// 4. Simulation loop to drive the agent.
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for range ticker.C {
		state := actor.State()
		fmt.Printf("Current State: %s\n", state)

		switch state {
		case "approving":
			fmt.Println("Action: Sending YES")
			actor.Send("YES")
		case "fixing":
			// If we've already retried once, let's succeed this time.
			if actor.Snapshot().Context.FixAttempts < 1 {
				fmt.Println("Action: Sending RETRY")
				actor.Send("RETRY")
			} else {
				fmt.Println("Action: Sending SUCCESS")
				actor.Send("SUCCESS")
			}
		case "verifying":
			fmt.Println("Action: Sending PASS")
			actor.Send("PASS")
		case "done":
			fmt.Println("\nSimulation finished.")
			return
		}
	}
}
