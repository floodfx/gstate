package main

import (
	"context"
	"fmt"
	"time"

	"github.com/floodfx/gstate"
)

type MyState string
type MyEvent string

const (
	StateInvestigating MyState = "investigating"
	StateApproving     MyState = "approving"
	StateFixing        MyState = "fixing"
	StateVerifying     MyState = "verifying"
	StateDone          MyState = "done"
)

const (
	EventYes     MyEvent = "YES"
	EventNo      MyEvent = "NO"
	EventSuccess MyEvent = "SUCCESS"
	EventRetry   MyEvent = "RETRY"
	EventFail    MyEvent = "FAIL"
	EventPass    MyEvent = "PASS"
)

// MyContext holds the state of our autonomous agent.
type MyContext struct {
	Retries     int
	FixAttempts int
	RepoDir     string
}

func main() {
	machine := gstate.New[MyState, MyEvent, MyContext]("agent").
		Initial(StateInvestigating).
		State(StateInvestigating, func(s *gstate.StateBuilder[MyState, MyEvent, MyContext]) {
			s.Invoke(func(ctx context.Context, c MyContext) error {
				fmt.Println("-> [investigating] Running diagnostics...")
				time.Sleep(100 * time.Millisecond)
				return nil
			}, StateApproving, StateDone)
		}).
		State(StateApproving, func(s *gstate.StateBuilder[MyState, MyEvent, MyContext]) {
			s.On(EventYes).GoTo(StateFixing)
			s.On(EventNo).GoTo(StateDone)
		}).
		State(StateFixing, func(s *gstate.StateBuilder[MyState, MyEvent, MyContext]) {
			s.Always().
				Guard(func(c MyContext) bool { return c.FixAttempts >= 2 }).
				Assign(func(c MyContext) MyContext {
					fmt.Println("-> [fixing] Max fix attempts reached. Giving up.")
					return c
				}).
				GoTo(StateDone)

			s.On(EventSuccess).GoTo(StateVerifying)
			
			s.On(EventRetry).
				Assign(func(c MyContext) MyContext {
					c.FixAttempts++
					fmt.Printf("-> [fixing] Retry attempt %d\n", c.FixAttempts)
					return c
				}).
				GoTo(StateInvestigating)
		}).
		State(StateVerifying, func(s *gstate.StateBuilder[MyState, MyEvent, MyContext]) {
			s.On(EventFail).
				Guard(func(c MyContext) bool { return c.Retries < 2 }).
				Assign(func(c MyContext) MyContext {
					c.Retries++
					fmt.Printf("-> [verifying] Retry count: %d\n", c.Retries)
					return c
				}).
				GoTo(StateFixing)

			s.On(EventFail).GoTo(StateDone)
			s.On(EventPass).GoTo(StateDone)
		}).
		State(StateDone, func(s *gstate.StateBuilder[MyState, MyEvent, MyContext]) {
			s.Type(gstate.Final)
			s.Entry(func(c MyContext) MyContext {
				fmt.Println("-> [done] Agent workflow complete.")
				return c
			})
		}).
		Build()

	fmt.Println("--- Starting Agent Actor ---")
	actor := gstate.Start(machine, MyContext{RepoDir: "./workspace"})

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for range ticker.C {
		state := actor.State()
		fmt.Printf("Current State: %s\n", state)

		switch state {
		case StateApproving:
			fmt.Println("Action: Sending YES")
			actor.Send(EventYes)
		case StateFixing:
			if actor.Snapshot().Context.FixAttempts < 1 {
				fmt.Println("Action: Sending RETRY")
				actor.Send(EventRetry)
			} else {
				fmt.Println("Action: Sending SUCCESS")
				actor.Send(EventSuccess)
			}
		case StateVerifying:
			fmt.Println("Action: Sending PASS")
			actor.Send(EventPass)
		case StateDone:
			fmt.Println("\nSimulation finished.")
			return
		}
	}
}
